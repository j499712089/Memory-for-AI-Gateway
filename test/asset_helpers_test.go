package test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gateway/internal/db"
)

// newAssetTeamDB opens (or creates) the team-1 database with both the Phase 3a
// assets schema and the Phase 3b sub-track schema applied.
func newAssetTeamDB(t *testing.T, database *db.DB, root string) *sql.DB {
	t.Helper()
	teamDB, err := db.OpenTeamDB(filepath.Join(root, "90_运行数据", "teams"), "team-1")
	if err != nil {
		t.Fatalf("open team database: %v", err)
	}
	t.Cleanup(func() { teamDB.Close() })
	if err := db.EnsureAssetsSchema(teamDB); err != nil {
		t.Fatalf("ensure assets schema: %v", err)
	}
	if err := db.EnsureAssetSubTracksSchema(teamDB); err != nil {
		t.Fatalf("ensure asset sub-track schema: %v", err)
	}
	return teamDB
}

// seedTeam inserts the minimal team row required by the global schema, plus a
// matching agent row so queue jobs carrying agent_id satisfy their FK.
func seedTeam(t *testing.T, database *db.DB) {
	t.Helper()
	if _, err := database.Global.Exec(`INSERT OR IGNORE INTO teams (id, name, slug) VALUES ('team-1', 'Team 1', 'team-1')`); err != nil {
		t.Fatalf("seed team: %v", err)
	}
	if _, err := database.Global.Exec(`INSERT OR IGNORE INTO agents (id, name, team_id) VALUES ('agent-1', 'Agent 1', 'team-1')`); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
}

// initGitRepo creates a git repository with the given .gitignore content and
// an initial commit, returning its path.
func initGitRepo(t *testing.T, gitignore string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(gitignore), 0644); err != nil {
			t.Fatalf("write .gitignore: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".keep"), []byte("base"), 0644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", repo,
		"-c", "user.name=Test", "-c", "user.email=test@local",
		"-c", "core.quotepath=false",
	}, args...)
	output, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}

func gitHead(t *testing.T, repo string) string {
	t.Helper()
	return firstLine(runGit(t, repo, "rev-parse", "HEAD"))
}

func firstLine(value string) string {
	for index := 0; index < len(value); index++ {
		if value[index] == '\n' {
			return value[:index]
		}
	}
	return value
}
