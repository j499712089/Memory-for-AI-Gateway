package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
	// Only staged content is committed. Untracked files (e.g. a stray file not
	// covered by .gitignore) are never committed, so "nothing staged" is a
	// clean no-op rather than a git commit error.
	staged, err := exec.Command("git", "-C", vaultPath, "diff", "--cached", "--name-only").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff --cached: %v: %s", err, strings.TrimSpace(string(staged)))
	}
	if strings.TrimSpace(string(staged)) == "" {
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

// LastGitCommit returns the most recent committed_at timestamp of a committed
// git batch. A zero time is returned when no batch has ever been committed, so
// a fresh vault is always considered due for its initial commit.
func LastGitCommit(ctx context.Context, globalDB *sql.DB) (time.Time, error) {
	if globalDB == nil {
		return time.Time{}, fmt.Errorf("git batch database is nil")
	}
	var at sql.NullString
	if err := globalDB.QueryRowContext(ctx, `SELECT MAX(committed_at) FROM git_batches WHERE status='committed'`).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if !at.Valid || at.String == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, at.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse last git commit time %q: %w", at.String, err)
	}
	return parsed, nil
}

// HasPendingGitCommit reports whether a git_commit job is already queued or
// being processed. Scheduled and post-promotion triggers use it so concurrent
// enqueues never stack duplicate git_commit jobs.
func HasPendingGitCommit(ctx context.Context, globalDB *sql.DB) (bool, error) {
	if globalDB == nil {
		return false, fmt.Errorf("git batch database is nil")
	}
	var count int
	if err := globalDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE queue='git_commit' AND status IN ('pending','processing')`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// EnqueueScheduledGitCommit enqueues a git_commit job when the 10-minute
// batching interval has elapsed since the last committed batch and no
// git_commit job is already pending. It returns (true, nil) when a job was
// enqueued.
func EnqueueScheduledGitCommit(ctx context.Context, queue *Queue, globalDB *sql.DB, now time.Time) (bool, error) {
	if queue == nil {
		return false, fmt.Errorf("queue is nil")
	}
	last, err := LastGitCommit(ctx, globalDB)
	if err != nil {
		return false, err
	}
	if !ShouldCommit(now, last) {
		return false, nil
	}
	pending, err := HasPendingGitCommit(ctx, globalDB)
	if err != nil {
		return false, err
	}
	if pending {
		return false, nil
	}
	if _, err := EnqueueGitCommit(ctx, queue, GitCommitPayload{Reason: "scheduled 10-minute batch commit"}); err != nil {
		return false, err
	}
	return true, nil
}

// RunGitCommitScheduler periodically checks whether the 10-minute batching
// interval has elapsed and enqueues a git_commit job when it has. The first
// check runs immediately so a fresh vault gets its initial commit without
// waiting out a full tick. It never queues a duplicate while one git_commit
// job is pending or processing.
func RunGitCommitScheduler(ctx context.Context, queue *Queue, globalDB *sql.DB, interval time.Duration, now func() time.Time) {
	if queue == nil || globalDB == nil {
		log.Println("git commit scheduler: queue and database are required")
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if now == nil {
		now = time.Now
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		enqueued, err := EnqueueScheduledGitCommit(ctx, queue, globalDB, now())
		switch {
		case err != nil && ctx.Err() == nil:
			log.Printf("git commit scheduler: %v", err)
		case enqueued:
			log.Println("git commit scheduler: enqueued scheduled vault batch commit")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
