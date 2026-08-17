package wiki

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gateway/internal/paths"
)

// linkPattern matches Obsidian wiki links: [[target]] and [[target|alias]].
// The alias is dropped for slug resolution but preserved in rendered text.
var linkPattern = regexp.MustCompile(`\[\[([^\[\]|]+)(?:\|([^\[\]]+))?\]\]`)

// Link is a single parsed internal link.
type Link struct {
	Target string // raw target before | alias
	Alias  string // display alias (empty when none)
}

// ParseLinks extracts all [[internal links]] from markdown content.
func ParseLinks(content string) []Link {
	matches := linkPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	links := make([]Link, 0, len(matches))
	for _, match := range matches {
		link := Link{Target: strings.TrimSpace(match[1])}
		if len(match) > 2 {
			link.Alias = strings.TrimSpace(match[2])
		}
		if link.Target != "" {
			links = append(links, link)
		}
	}
	return links
}

// TargetSlugs converts parsed links into their resolved slugs.
func TargetSlugs(links []Link) []string {
	if len(links) == 0 {
		return nil
	}
	slugs := make([]string, 0, len(links))
	for _, link := range links {
		if slug := Slugify(link.Target); slug != "" {
			slugs = append(slugs, slug)
		}
	}
	return slugs
}

// Slugify converts a page title into a stable, URL-safe slug.
func Slugify(title string) string {
	value := strings.ToLower(strings.TrimSpace(title))
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.ReplaceAll(value, "_", "-")
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-':
			builder.WriteRune(char)
		default:
			// Drop CJK and punctuation from slugs: obsidian titles keep the
			// original form; slugs stay ASCII for stable edge lookups.
		}
	}
	slug := builder.String()
	slug = strings.Trim(slug, "-")
	slug = regexp.MustCompile(`-+`).ReplaceAllString(slug, "-")
	return slug
}

// Frontmatter is the YAML-style block rendered at the top of every wiki page.
type Frontmatter struct {
	Title    string   `json:"title"`
	AssetID  string   `json:"asset_id"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Source   []string `json:"source_event_ids,omitempty"`
	Modified string   `json:"modified"`
}

// RenderFrontmatter serialises the metadata block in Obsidian-compatible YAML.
func RenderFrontmatter(meta Frontmatter) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString(fmt.Sprintf("title: %q\n", meta.Title))
	if meta.AssetID != "" {
		builder.WriteString(fmt.Sprintf("asset_id: %q\n", meta.AssetID))
	}
	if meta.Type != "" {
		builder.WriteString(fmt.Sprintf("type: %q\n", meta.Type))
	}
	if meta.Status != "" {
		builder.WriteString(fmt.Sprintf("status: %q\n", meta.Status))
	}
	if len(meta.Source) > 0 {
		quoted := make([]string, 0, len(meta.Source))
		for _, id := range meta.Source {
			quoted = append(quoted, fmt.Sprintf("%q", id))
		}
		builder.WriteString("source_event_ids: [" + strings.Join(quoted, ", ") + "]\n")
	}
	if meta.Modified != "" {
		builder.WriteString(fmt.Sprintf("modified: %q\n", meta.Modified))
	}
	builder.WriteString("---\n")
	return builder.String()
}

// ComposePage builds the full markdown body for a wiki page: frontmatter,
// an H1 heading, the summary block, and the original content.
func ComposePage(title, summary, content string) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("# %s\n\n", title))
	if strings.TrimSpace(summary) != "" {
		builder.WriteString(summary + "\n\n")
	}
	if strings.TrimSpace(content) != "" {
		builder.WriteString(content + "\n")
	}
	return builder.String()
}

// VaultWriter writes generated markdown assets into the Obsidian vault under
// a fixed relative directory, refusing to escape the memory root.
type VaultWriter struct {
	MemoryRoot string
}

// NewVaultWriter returns a writer pinned to the vault root.
func NewVaultWriter(memoryRoot string) (*VaultWriter, error) {
	if memoryRoot == "" {
		return nil, fmt.Errorf("memory root is required")
	}
	root, err := filepath.Abs(memoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve memory root: %w", err)
	}
	return &VaultWriter{MemoryRoot: root}, nil
}

// WriteMarkdown safely writes content under <root>/<relativeDir>/<name>.
// It creates the directory tree on demand and refuses paths that traverse
// outside the vault (defense in depth for worker-generated files).
func (w *VaultWriter) WriteMarkdown(relativeDir, name string, content []byte) (string, error) {
	if w == nil || w.MemoryRoot == "" {
		return "", fmt.Errorf("vault writer is not initialised")
	}
	if relativeDir == "" || name == "" {
		return "", fmt.Errorf("relative directory and file name are required")
	}
	if filepath.IsAbs(relativeDir) || filepath.IsAbs(name) || strings.Contains(name, "..") {
		return "", fmt.Errorf("unsafe vault path: %s / %s", relativeDir, name)
	}
	fullDir, err := paths.SafeJoin(w.MemoryRoot, relativeDir)
	if err != nil {
		return "", fmt.Errorf("unsafe wiki directory %q: %w", relativeDir, err)
	}
	if err := os.MkdirAll(fullDir, 0755); err != nil {
		return "", fmt.Errorf("create wiki directory: %w", err)
	}
	fullPath := filepath.Join(fullDir, name)
	// Write through a temp file and rename so a partial write never lands in
	// the vault (Obsidian and the git watcher only see complete files).
	tmp := fullPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return "", fmt.Errorf("write wiki temp file: %w", err)
	}
	if err := os.Rename(tmp, fullPath); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("finalise wiki file: %w", err)
	}
	rel, err := filepath.Rel(w.MemoryRoot, fullPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
