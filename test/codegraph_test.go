package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/codegraph"
	"gateway/internal/db"
	"gateway/internal/worker"
)

// TestCodeGraphFallbackParserExtractsSymbolsAndCalls verifies the parser
// (pure-Go fallback in this environment) extracts functions and call edges.
func TestCodeGraphFallbackParserExtractsSymbolsAndCalls(t *testing.T) {
	parser := codegraph.NewParser()
	source := `package main

import "fmt"

func greet(name string) string {
	fmt.Println("hello " + name)
	return name
}

func main() {
	greet("world")
}
`
	result, err := parser.Parse("go", []byte(source))
	if err != nil {
		t.Fatalf("parse go source: %v", err)
	}
	var hasGreet, hasMain bool
	for _, symbol := range result.Symbols {
		if symbol.Name == "greet" && symbol.Kind == "function" {
			hasGreet = true
		}
		if symbol.Name == "main" && symbol.Kind == "function" {
			hasMain = true
		}
	}
	if !hasGreet || !hasMain {
		t.Fatalf("expected greet and main functions, got %+v", result.Symbols)
	}
	var mainCallsGreet bool
	for _, edge := range result.Edges {
		if edge.Source == "main" && edge.Target == "greet" && edge.Type == "call" {
			mainCallsGreet = true
		}
	}
	if !mainCallsGreet {
		t.Fatalf("expected main -> greet call edge, got %+v", result.Edges)
	}
}

// TestCodeGraphIncrementalHashSkipAndDiff verifies:
//   - first index parses the repo
//   - unchanged files are skipped on the second pass (hash check)
//   - a modified file is re-indexed via git diff
func TestCodeGraphIncrementalHashSkipAndDiff(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	repo := initGitRepo(t, "")
	mainGo := filepath.Join(repo, "main.go")
	writeSource := func(content string) {
		if err := os.WriteFile(mainGo, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		runGit(t, repo, "add", "main.go")
		runGit(t, repo, "commit", "-m", "update main.go")
	}
	writeSource(`package main

func alpha() int { return 1 }
func beta() int { return alpha() }
func main() { beta() }
`)

	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, status, visibility) VALUES ('repo-asset', 'team-1', 'codegraph', 'TestRepo', 'test-repo', 'approved', 'team')`); err != nil {
		t.Fatal(err)
	}
	codeRepo, err := db.GetOrCreateCodeRepo(context.Background(), teamDB, db.CodeRepo{
		ID: "repo-1", AssetID: "repo-asset", RepoURL: "https://example.test/test", LocalPath: repo,
	})
	if err != nil {
		t.Fatalf("create code repo: %v", err)
	}

	indexer := codegraph.NewIncrementalIndexer(teamDB, codegraph.NewParser())
	first, err := indexer.IndexRepo(context.Background(), codeRepo)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	if first.FilesIndexed != 1 || first.Symbols < 3 {
		t.Fatalf("first index unexpected: %+v", first)
	}

	// Second pass: nothing changed -> no re-parse (hash/diff skip).
	second, err := indexer.IndexRepo(context.Background(), codeRepo)
	if err != nil {
		t.Fatalf("second index: %v", err)
	}
	if second.FilesIndexed != 0 {
		t.Fatalf("expected unchanged repo to skip re-indexing: %+v", second)
	}
	var symbolCountBefore int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM code_symbols`).Scan(&symbolCountBefore); err != nil {
		t.Fatal(err)
	}

	// Modify the file -> git diff catches it and re-parses.
	writeSource(`package main

func alpha() int { return 1 }
func beta() int { return alpha() + 1 }
func gamma() int { return beta() }
func main() { gamma() }
`)
	third, err := indexer.IndexRepo(context.Background(), codeRepo)
	if err != nil {
		t.Fatalf("third index: %v", err)
	}
	if third.FilesIndexed != 1 {
		t.Fatalf("expected modified file re-indexed: %+v", third)
	}
	var gammaCount int
	if err := teamDB.QueryRow(`SELECT COUNT(*) FROM code_symbols WHERE name='gamma'`).Scan(&gammaCount); err != nil {
		t.Fatal(err)
	}
	if gammaCount != 1 {
		t.Fatalf("expected gamma symbol after diff reindex, got %d", gammaCount)
	}

	// Cursor must have advanced to HEAD.
	cursor, err := db.GetCodeGraphCursor(context.Background(), teamDB, "repo-1")
	if err != nil {
		t.Fatal(err)
	}
	if cursor.LastIndexedCommit == "" {
		t.Fatal("cursor did not persist head commit")
	}
}

// TestCodeGraphImpactReturnsCallChain verifies codegraph_impact: given a
// symbol, callers/callees are returned with file paths.
func TestCodeGraphImpactReturnsCallChain(t *testing.T) {
	database, root := phase3Database(t)
	defer database.Close()
	teamDB := newAssetTeamDB(t, database, root)

	repo := initGitRepo(t, "")
	if err := os.WriteFile(filepath.Join(repo, "app.go"), []byte(`package app

func root() {}
func middle() { root() }
func leaf() { middle() }
`), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "app.go")
	runGit(t, repo, "commit", "-m", "add app.go")

	if _, err := teamDB.Exec(`INSERT INTO assets (id, team_id, asset_type, name, slug, status, visibility) VALUES ('impact-asset', 'team-1', 'codegraph', 'App', 'app', 'approved', 'team')`); err != nil {
		t.Fatal(err)
	}
	codeRepo, err := db.GetOrCreateCodeRepo(context.Background(), teamDB, db.CodeRepo{
		ID: "repo-impact", AssetID: "impact-asset", LocalPath: repo,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.IndexRepo(context.Background(), teamDB, codeRepo, codegraph.NewParser()); err != nil {
		t.Fatalf("index impact repo: %v", err)
	}

	impact, err := codegraph.Impact(context.Background(), teamDB, "repo-impact", "", "middle", 3)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if impact.Symbol.Name != "middle" {
		t.Fatalf("unexpected symbol: %+v", impact.Symbol)
	}
	hasCaller := false
	for _, caller := range impact.Callers {
		if caller.Name == "leaf" && strings.Contains(caller.FilePath, "app.go") {
			hasCaller = true
		}
	}
	if !hasCaller {
		t.Fatalf("expected leaf as caller of middle: callers=%+v", impact.Callers)
	}
}
