package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gateway/internal/db"
	"gateway/internal/idgen"
)

// commitAuthor is used inline so the worker never depends on a machine-wide
// git identity. The commit is strictly local — push is forbidden by the
// Constitution and this worker never calls it.
const (
	commitAuthorName  = "Memory Gateway Worker"
	commitAuthorEmail = "memory@local"
	commitInterval    = 10 * time.Minute
)

// allowedVaultDirs are the asset directories eligible for batch commits.
// L0/.runtime/gateway/keys are excluded by .gitignore and never added.
var allowedVaultDirs = []string{
	"L1_任务纪要",
	"L2_知识经验",
	"L3_团队身份",
	"L4_长期准则",
	"02_Wiki知识库",
	"03_代码关系图",
	"04_技能库",
	"05_团队与代理",
}

// GitCommitPayload triggers one vault batch commit.
type GitCommitPayload struct {
	TeamID  string `json:"team_id"`
	Reason  string `json:"reason"`
	BatchID string `json:"batch_id"`
}

// EnqueueGitCommit queues a git_commit job. It is normally enqueued after an
// asset promotion or by the scheduler when the commit interval elapsed.
func EnqueueGitCommit(ctx context.Context, queue *Queue, payload GitCommitPayload) (string, error) {
	if queue == nil {
		return "", fmt.Errorf("queue is nil")
	}
	if payload.BatchID == "" {
		payload.BatchID = idgen.NewID()
	}
	return queue.Enqueue(ctx, Job{
		Queue:        "git_commit",
		TeamID:       payload.TeamID,
		PartitionKey: "git:vault",
		Payload:      payload,
	})
}

// ShouldCommit reports whether a commit is due: now is at least 10 minutes
// after lastCommit, or there has never been a commit.
func ShouldCommit(now, lastCommit time.Time) bool {
	if lastCommit.IsZero() {
		return true
	}
	return now.Sub(lastCommit) >= commitInterval
}

// CollectCommitFiles lists the relative paths under the vault that are
// eligible for batch commits. Only the allowed asset directories are
// considered; files excluded by .gitignore stay out of git entirely.
func CollectCommitFiles(vaultPath string) ([]string, error) {
	var files []string
	for _, dir := range allowedVaultDirs {
		root := filepath.Join(vaultPath, dir)
		info, err := os.Stat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.Walk(root, func(path string, entry os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(vaultPath, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

// CommitVault stages the allowed asset directories and creates one local
// commit. It returns the commit sha, or an empty string when there was
// nothing to commit. Push is never performed.
func CommitVault(vaultPath, message string) (string, error) {
	if vaultPath == "" {
		return "", fmt.Errorf("vault path is required")
	}
	if _, err := os.Stat(filepath.Join(vaultPath, ".git")); err != nil {
		return "", fmt.Errorf("vault %s is not a git repository: %w", vaultPath, err)
	}
	existingDirs := make([]string, 0, len(allowedVaultDirs))
	for _, dir := range allowedVaultDirs {
		if info, err := os.Stat(filepath.Join(vaultPath, dir)); err == nil && info.IsDir() {
			existingDirs = append(existingDirs, dir)
		}
	}
	if len(existingDirs) == 0 {
		return "", nil // no eligible asset directory -> nothing to commit
	}
	args := append([]string{"-C", vaultPath, "add", "--"}, existingDirs...)
	if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %v: %s", err, strings.TrimSpace(string(output)))
	}
	statusOutput, err := exec.Command("git", "-C", vaultPath, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(statusOutput)) == "" {
		return "", nil // nothing staged
	}
	if strings.TrimSpace(message) == "" {
		message = "memory: batch asset commit"
	}
	commitArgs := []string{"-C", vaultPath,
		"-c", "user.name=" + commitAuthorName,
		"-c", "user.email=" + commitAuthorEmail,
		"commit", "-m", message,
	}
	if output, err := exec.Command("git", commitArgs...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %v: %s", err, strings.TrimSpace(string(output)))
	}
	head, err := exec.Command("git", "-C", vaultPath, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(head)), nil
}

// RunGitBatchCommit drives the whole git_commit handler: it runs the commit,
// records the result in git_batches (global db) and returns the sha.
func RunGitBatchCommit(ctx context.Context, globalDB *sql.DB, vaultPath string, payload GitCommitPayload) (string, error) {
	if globalDB == nil {
		return "", fmt.Errorf("git batch database is nil")
	}
	batchID := payload.BatchID
	if batchID == "" {
		batchID = idgen.NewID()
	}
	if err := db.CreateGitBatch(ctx, globalDB, db.GitBatch{ID: batchID, Status: "pending", Message: payload.Reason}); err != nil {
		return "", fmt.Errorf("create git batch: %w", err)
	}
	sha, err := CommitVault(vaultPath, fmt.Sprintf("memory: %s", payload.Reason))
	if err != nil {
		_ = db.MarkGitBatchFailed(ctx, globalDB, batchID, err.Error())
		return "", err
	}
	if sha == "" {
		// Nothing to commit is a legitimate outcome, not a failure.
		_ = db.MarkGitBatchCommitted(ctx, globalDB, batchID, "", "no changes")
		return "", nil
	}
	if err := db.MarkGitBatchCommitted(ctx, globalDB, batchID, sha, fmt.Sprintf("memory: %s", payload.Reason)); err != nil {
		return "", err
	}
	return sha, nil
}

func registerGitCommit(processor *Processor, deps AssetWorkerDeps) {
	processor.Register("git_commit", func(ctx context.Context, claim *Claim) error {
		var payload GitCommitPayload
		if err := json.Unmarshal([]byte(claim.PayloadJSON), &payload); err != nil {
			return fmt.Errorf("git_commit payload: %w", err)
		}
		if deps.VaultPath == "" {
			return fmt.Errorf("git_commit worker requires a vault path")
		}
		_, err := RunGitBatchCommit(ctx, deps.GlobalDB, deps.VaultPath, payload)
		return err
	})
}
