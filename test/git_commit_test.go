package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gateway/internal/worker"
)

const vaultGitignore = `# Runtime data
L0_原始记录/
.runtime/
teams/*/memory.db
07_索引/
06_事件队列/
90_运行数据/
gateway/

# Secret files
*.key
*.pem
secrets/
.env
`

// TestGitCommitWorkerCommittedBatch verifies the git_commit worker: it stages
// the allowed asset dirs, commits locally, and records the sha in git_batches
// with status 'committed'.
func TestGitCommitWorkerCommittedBatch(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	seedTeam(t, database)
	vault := initGitRepo(t, vaultGitignore)

	// Sensitive paths that must never enter the commit.
	mustExclude := []string{
		filepath.Join(vault, "L0_原始记录", "raw.jsonl"),
		filepath.Join(vault, ".runtime", "memory-gateway.db"),
		filepath.Join(vault, "gateway", "main.go"),
		filepath.Join(vault, "secret.key"),
	}
	for _, path := range mustExclude {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sensitive"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Eligible asset files.
	eligible := []string{
		filepath.Join(vault, "L1_任务纪要", "fact.md"),
		filepath.Join(vault, "L2_知识经验", "retro.md"),
		filepath.Join(vault, "L3_团队身份", "card.md"),
		filepath.Join(vault, "L4_长期准则", "rule.md"),
		filepath.Join(vault, "02_Wiki知识库", "团队共享", "page.md"),
		filepath.Join(vault, "04_技能库", "team", "skill.md"),
	}
	for _, path := range eligible {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	queue := worker.NewQueue(database.Global, 0)
	processor := worker.NewProcessor(queue)
	deps := worker.AssetWorkerDeps{GlobalDB: database.Global, VaultPath: vault}
	if err := worker.RegisterAssetWorkers(processor, deps); err != nil {
		t.Fatalf("register asset workers: %v", err)
	}
	if _, err := worker.EnqueueGitCommit(context.Background(), queue, worker.GitCommitPayload{TeamID: "team-1", Reason: "first vault commit"}); err != nil {
		t.Fatalf("enqueue git commit: %v", err)
	}
	processed, err := processor.ProcessOnce(context.Background(), "worker-1")
	if err != nil {
		t.Fatalf("process git commit: %v", err)
	}
	if !processed {
		t.Fatal("expected a git_commit job to be processed")
	}

	var status, sha string
	if err := database.Global.QueryRow(`SELECT status, COALESCE(commit_sha,'') FROM git_batches ORDER BY created_at DESC LIMIT 1`).Scan(&status, &sha); err != nil {
		t.Fatal(err)
	}
	if status != "committed" || sha == "" {
		t.Fatalf("batch not committed: status=%s sha=%s", status, sha)
	}

	// The commit must contain the eligible files and none of the excluded ones.
	show := runGit(t, vault, "show", "--stat", "--oneline", sha)
	for _, path := range eligible {
		rel, _ := filepath.Rel(vault, path)
		if !strings.Contains(show, filepath.ToSlash(rel)) {
			t.Fatalf("eligible file %s missing from commit:\n%s", rel, show)
		}
	}
	for _, path := range mustExclude {
		rel, _ := filepath.Rel(vault, path)
		if strings.Contains(show, filepath.ToSlash(rel)) {
			t.Fatalf("excluded path %s leaked into commit:\n%s", rel, show)
		}
	}
	// The commit should be strictly local — no remote configured.
	remotes := strings.TrimSpace(runGit(t, vault, "remote", "-v"))
	if remotes != "" {
		t.Fatalf("git_commit worker must never push: remotes=%s", remotes)
	}
}

// TestGitCommitNoChangesIsNotAnError verifies an idle vault yields a clean
// no-op (status committed with empty sha) instead of a failure.
func TestGitCommitNoChangesIsNotAnError(t *testing.T) {
	database, _ := phase3Database(t)
	defer database.Close()
	vault := initGitRepo(t, vaultGitignore)

	sha, err := worker.RunGitBatchCommit(context.Background(), database.Global, vault, worker.GitCommitPayload{BatchID: "batch-noop", Reason: "idle"})
	if err != nil {
		t.Fatalf("idle commit should not error: %v", err)
	}
	if sha != "" {
		t.Fatalf("expected no commit for idle vault, got sha %s", sha)
	}
}

// TestGitShouldCommitInterval verifies the 10-minute batching interval.
func TestGitShouldCommitInterval(t *testing.T) {
	now := time.Now()
	if !worker.ShouldCommit(now, time.Time{}) {
		t.Fatal("first commit should always be due")
	}
	if worker.ShouldCommit(now, now.Add(-5*time.Minute)) {
		t.Fatal("commit should not be due before 10 minutes")
	}
	if !worker.ShouldCommit(now, now.Add(-11*time.Minute)) {
		t.Fatal("commit should be due after 10 minutes")
	}
}
