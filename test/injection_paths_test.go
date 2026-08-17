package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gateway/internal/adapter"
	"gateway/internal/paths"
)

func TestSafeJoinEnforcesPathBoundary(t *testing.T) {
	root := t.TempDir()
	allowed, err := paths.SafeJoin(root, filepath.Join("00_系统", "路径注入清单.md"))
	if err != nil {
		t.Fatalf("expected normal child path to be allowed: %v", err)
	}
	expected, err := filepath.Abs(filepath.Join(root, "00_系统", "路径注入清单.md"))
	if err != nil || allowed != filepath.Clean(expected) {
		t.Fatalf("unexpected safe path: got %q want %q", allowed, expected)
	}

	sibling := filepath.Join("..", filepath.Base(root)+"-sibling", "secret.md")
	for _, escaped := range []string{
		filepath.Join("..", "outside", "secret.md"),
		sibling,
		filepath.Join(filepath.Dir(root), "outside", "secret.md"),
	} {
		if _, err := paths.SafeJoin(root, escaped); err == nil {
			t.Errorf("expected path to be rejected: %q", escaped)
		}
	}

	// Internal writers may validate an already-built absolute path, but it
	// must still remain below the configured root.
	if err := paths.AssertSafePath(root, allowed); err != nil {
		t.Fatalf("absolute child path should pass AssertSafePath: %v", err)
	}

	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink test unavailable: %v", err)
	}
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := paths.SafeJoin(root, filepath.Join("link", "secret.md")); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestCompatibilityInjectionPackageUsesAuditableManifest(t *testing.T) {
	pkg := adapter.BuildInjectionPackage("team-1\r\n", "agent-1\n", "operator\r\nadmin")
	if pkg.ManifestVersion != adapter.ManifestVersion || pkg.ManifestVersion == "phase2-placeholder" {
		t.Fatalf("unexpected manifest version: %q", pkg.ManifestVersion)
	}
	for _, label := range []string{"MEMORY_ROOT", "CHAT_MEMORY", "WIKI", "CODE_GRAPH", "SKILLS", "TEAM_AGENT", "EVENT_QUEUE", "INDEX", "RUNTIME"} {
		if !strings.Contains(pkg.PathManifest, label) {
			t.Errorf("manifest missing label %q", label)
		}
	}
	if strings.Contains(pkg.IdentityCard, "\r") || strings.Contains(pkg.IdentityCard, "operator\nadmin") {
		t.Fatalf("identity card contains unsanitized line break: %q", pkg.IdentityCard)
	}
	pkg.SourceEventIDs = []string{"event-1", "event-2"}
	rendered := adapter.RenderInjectionText(pkg)
	if !strings.Contains(rendered, "source_ids: [event-1,event-2]") || !strings.Contains(rendered, "manifest_version: "+adapter.ManifestVersion) {
		t.Fatalf("rendered package is not auditable: %q", rendered)
	}
}
