package adapter

import (
	"fmt"
	"strings"
)

// BuildInjectionPackage assembles the memory injection content
// Phase 2: Uses fixed path list placeholder, no real retrieval
func BuildInjectionPackage(teamID, agentID, identityCardName string) InjectionPackage {
	// Fixed path list placeholder for Phase 2
	pathManifest := `## Memory Context Paths (Phase 2 Placeholder)
- L0_原始记录/
- L1_任务纪要/
- L2_知识经验/
- L3_团队身份/
- L4_长期准则/`

	// Identity card summary (placeholder)
	identityCard := ""
	if identityCardName != "" {
		identityCard = fmt.Sprintf("\n## Agent Identity\nRole: %s", identityCardName)
	}

	return InjectionPackage{
		PathManifest:    pathManifest,
		IdentityCard:    identityCard,
		RetrievalSnips:  []string{}, // Phase 3 will populate
		SourceEventIDs:  []string{},
		ManifestVersion: "phase2-placeholder",
		Truncated:       false,
	}
}

// RenderInjectionText converts injection package to text for system/instructions
func RenderInjectionText(pkg InjectionPackage) string {
	var parts []string

	if pkg.PathManifest != "" {
		parts = append(parts, pkg.PathManifest)
	}

	if pkg.IdentityCard != "" {
		parts = append(parts, pkg.IdentityCard)
	}

	// Phase 3 will add retrieval snippets here
	if len(pkg.RetrievalSnips) > 0 {
		parts = append(parts, "\n## Retrieved Context")
		parts = append(parts, pkg.RetrievalSnips...)
	}

	parts = append(parts, fmt.Sprintf("\n<!-- manifest_version: %s -->", pkg.ManifestVersion))
	if pkg.Truncated {
		parts = append(parts, "<!-- memory_truncated: true -->")
	}

	return strings.Join(parts, "\n")
}
