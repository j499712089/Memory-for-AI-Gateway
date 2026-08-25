package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// TeamsDir resolves the directory under which per-team runtime databases are
// stored. Per the Memory Plus file-path convention, team runtime data belongs
// in the 90_运行数据 ($RUNTIME) layer, not the memory root itself. The value is
// overridable via TEAMS_DIR for deployment-specific layouts; when unset it
// defaults to <memoryRoot>/90_运行数据/teams.
func TeamsDir(memoryRoot string) string {
	if env := strings.TrimSpace(os.Getenv("TEAMS_DIR")); env != "" {
		return env
	}
	return filepath.Join(memoryRoot, "90_运行数据", "teams")
}
