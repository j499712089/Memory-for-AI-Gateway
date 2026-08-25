package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/db"
	"gateway/internal/wiki"
	"gateway/internal/worker"
)

// TestRealVaultPhase3bWorkerCommit is an OPT-IN integration test against the
// real vault (F:\memory_plus). It is skipped unless REAL_VAULT_WORKER_TEST=1.
//
// It renders one L1 asset markdown through the Phase 3b worker path and runs
// a git batch commit, then asserts the commit sha is recorded and that no
// sensitive paths (L0 / .runtime / gateway / secrets) entered the commit.
func TestRealVaultPhase3bWorkerCommit(t *testing.T) {
	if os.Getenv("REAL_VAULT_WORKER_TEST") != "1" {
		t.Skip("opt-in integration test; set REAL_VAULT_WORKER_TEST=1 to run")
	}
	vault := os.Getenv("MEMORY_PLUS_DIR")
	if vault == "" {
		vault = "F:\\memory_plus"
	}
	globalDB, err := db.Open(filepath.Join(vault, ".runtime", "memory-gateway.db"))
	if err != nil {
		t.Fatalf("open global db: %v", err)
	}
	defer globalDB.Close()

	// Render one L1 asset markdown through the same writer the l1_refine
	// worker uses (proves Obsidian-visible output from the sub-track).
	writer, err := wiki.NewVaultWriter(vault)
	if err != nil {
		t.Fatalf("vault writer: %v", err)
	}
	rel, err := worker.WriteAssetMarkdown(writer, "l1", "phase3b-l1-demo", "Phase 3b L1 Demo",
		"L1 asset rendered by the IMP-04 Phase 3b worker sub-track", []string{"phase3b-evidence"})
	if err != nil {
		t.Fatalf("render l1 markdown: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(vault, filepath.FromSlash(rel))) })

	sha, err := worker.RunGitBatchCommit(context.Background(), globalDB.Global, vault,
		worker.GitCommitPayload{TeamID: "real-vault", Reason: "IMP-04 Phase 3b asset sub-track"})
	if err != nil {
		t.Fatalf("git batch commit: %v", err)
	}
	if sha == "" {
		t.Fatal("expected a real commit sha")
	}
	var status string
	if err := globalDB.Global.QueryRow(`SELECT status FROM git_batches ORDER BY created_at DESC LIMIT 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "committed" {
		t.Fatalf("batch status = %s, want committed", status)
	}

	// Sensitive paths must never appear in any tracked file.
	output, err := exec.Command("git", "-C", vault, "ls-files").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{"L0_原始记录", ".runtime", "gateway/", "secrets/", ".key", ".pem", ".env"} {
		if strings.Contains(string(output), pattern) {
			t.Fatalf("sensitive path %q is tracked in the vault:\n%s", pattern, output)
		}
	}
	t.Logf("vault commit sha=%s status=%s file=%s", sha, status, rel)
}
