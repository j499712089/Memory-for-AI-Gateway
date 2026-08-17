package codegraph

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gateway/internal/db"
	"gateway/internal/hashutil"
)

// IndexReport summarises one incremental pass over a repo.
type IndexReport struct {
	FilesScanned int    `json:"files_scanned"`
	FilesIndexed int    `json:"files_indexed"`
	FilesSkipped int    `json:"files_skipped"`
	Symbols      int    `json:"symbols"`
	Edges        int    `json:"edges"`
	HeadCommit   string `json:"head_commit"`
}

// IncrementalIndexer indexes a local repository into the codegraph tables.
// It skips files whose content hash is unchanged and re-parses only new or
// modified files, as required by the CodeGraph incremental sub-track.
type IncrementalIndexer struct {
	Database *sql.DB
	Parser   Parser
}

// NewIncrementalIndexer returns an indexer wired to the team database.
func NewIncrementalIndexer(database *sql.DB, parser Parser) *IncrementalIndexer {
	if parser == nil {
		parser = NewParser()
	}
	return &IncrementalIndexer{Database: database, Parser: parser}
}

// HashFile returns the sha256 of a file's bytes.
func HashFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashutil.SHA256Bytes(content), nil
}

// IndexRepo performs a full or incremental index of one repo:
//  1. resolve HEAD commit and load the stored cursor
//  2. walk supported source files, hashing each
//  3. skip unchanged hashes, parse changed/new files, replace their symbols
//  4. persist the cursor at HEAD
func (idx *IncrementalIndexer) IndexRepo(ctx context.Context, repo db.CodeRepo) (IndexReport, error) {
	if idx == nil || idx.Database == nil {
		return IndexReport{}, fmt.Errorf("incremental indexer database is nil")
	}
	if repo.LocalPath == "" {
		return IndexReport{}, fmt.Errorf("repo local path is required")
	}
	if _, err := os.Stat(repo.LocalPath); err != nil {
		return IndexReport{}, fmt.Errorf("repo path %s is not readable: %w", repo.LocalPath, err)
	}
	head, err := gitHead(ctx, repo.LocalPath)
	if err != nil {
		return IndexReport{}, fmt.Errorf("resolve head commit: %w", err)
	}
	report := IndexReport{HeadCommit: head}

	// Files that changed since the cursor, or every file on first index.
	targets, err := idx.changedFiles(ctx, repo, head)
	if err != nil {
		return IndexReport{}, err
	}
	for _, path := range targets {
		if DetectLanguage(path) == "" {
			report.FilesScanned++
			continue
		}
		report.FilesScanned++
		changed, err := idx.IndexFile(ctx, repo.ID, path)
		if err != nil {
			return report, err
		}
		if changed {
			report.FilesIndexed++
		} else {
			report.FilesSkipped++
		}
	}
	cursor, err := db.GetCodeGraphCursor(ctx, idx.Database, repo.ID)
	if err != nil {
		return report, err
	}
	cursor.RepoID = repo.ID
	cursor.LastIndexedCommit = head
	cursor.LastIndexedAt = nowUTC()
	cursor.PendingDiffs = nil
	if err := db.SetCodeGraphCursor(ctx, idx.Database, cursor); err != nil {
		return report, fmt.Errorf("persist codegraph cursor: %w", err)
	}
	// Count symbols/edges produced in this pass for the health panel.
	var symbolCount, edgeCount int
	_ = idx.Database.QueryRowContext(ctx, `SELECT COUNT(*) FROM code_symbols WHERE file_id IN (SELECT id FROM code_files WHERE repo_id=?)`, repo.ID).Scan(&symbolCount)
	_ = idx.Database.QueryRowContext(ctx, `SELECT COUNT(*) FROM code_edges WHERE source_symbol_id IN (SELECT id FROM code_symbols WHERE file_id IN (SELECT id FROM code_files WHERE repo_id=?))`, repo.ID).Scan(&edgeCount)
	report.Symbols = symbolCount
	report.Edges = edgeCount
	return report, nil
}

// IndexFile parses a single file and writes/updates its symbols+edges.
// It returns true when the file changed (new or hash mismatch). Files whose
// hash is unchanged are skipped — that is the hash-skip requirement.
func (idx *IncrementalIndexer) IndexFile(ctx context.Context, repoID, path string) (bool, error) {
	if idx == nil || idx.Database == nil {
		return false, fmt.Errorf("incremental indexer database is nil")
	}
	language := DetectLanguage(path)
	if language == "" {
		return false, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read source file %s: %w", path, err)
	}
	hash := hashutil.SHA256Bytes(content)
	fileID, created, err := db.GetOrCreateCodeFile(ctx, idx.Database, db.CodeFile{
		RepoID: repoID, Path: path, FileHash: hash, Language: language, SizeBytes: len(content),
	})
	if err != nil {
		return false, err
	}
	if !created {
		return false, nil // hash unchanged -> skip
	}
	result, err := idx.Parser.Parse(language, content)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	symbols := make([]db.CodeSymbol, 0, len(result.Symbols))
	for _, symbol := range result.Symbols {
		lineStart := symbol.LineStart
		lineEnd := symbol.LineEnd
		if lineEnd < lineStart {
			lineEnd = lineStart
		}
		symbols = append(symbols, db.CodeSymbol{
			ID:         "sym-" + fileID + "-" + symbol.Name + "-" + fmt.Sprint(lineStart),
			FileID:     fileID,
			Name:       symbol.Name,
			Kind:       symbol.Kind,
			Signature:  symbol.Signature,
			LineStart:  lineStart,
			LineEnd:    lineEnd,
			SymbolHash: hashutil.SHA256(symbol.Name + ":" + symbol.Kind + ":" + symbol.Signature),
		})
	}
	byID := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		byID[symbol.Name] = symbol.ID
	}
	edges := make([]db.CodeEdge, 0, len(result.Edges))
	for _, edge := range result.Edges {
		sourceID, okSource := byID[edge.Source]
		targetID, okTarget := byID[edge.Target]
		if !okSource || !okTarget {
			continue
		}
		edges = append(edges, db.CodeEdge{SourceSymbolID: sourceID, TargetSymbolID: targetID, EdgeType: edge.Type})
	}
	if err := db.ReplaceFileSymbols(ctx, idx.Database, fileID, symbols, edges); err != nil {
		return false, fmt.Errorf("store symbols for %s: %w", path, err)
	}
	return true, nil
}

// changedFiles returns the set of source files to process: files changed since
// the stored cursor (via git diff) or every supported file on a first index.
func (idx *IncrementalIndexer) changedFiles(ctx context.Context, repo db.CodeRepo, head string) ([]string, error) {
	cursor, err := db.GetCodeGraphCursor(ctx, idx.Database, repo.ID)
	if err != nil {
		return nil, err
	}
	if cursor.LastIndexedCommit == "" {
		return allSourceFiles(repo.LocalPath)
	}
	diff := fmt.Sprintf("%s..%s", cursor.LastIndexedCommit, head)
	output, err := gitOutput(ctx, repo.LocalPath, "diff", "--name-only", diff)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", diff, err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, filepath.Join(repo.LocalPath, filepath.FromSlash(line)))
		}
	}
	return files, nil
}

// allSourceFiles walks the repo tree collecting supported source files while
// skipping the .git directory and common vendor/build directories.
func allSourceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" || base == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		if DetectLanguage(path) != "" {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func gitHead(ctx context.Context, repoPath string) (string, error) {
	output, err := gitOutput(ctx, repoPath, "rev-parse", "HEAD")
	return strings.TrimSpace(output), err
}

func gitOutput(ctx context.Context, repoPath string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", repoPath}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
