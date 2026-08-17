package adapter

import (
	"fmt"
	"strings"
)

// ManifestVersion identifies the current injection contract. Keep this value
// in the adapter package so compatibility callers cannot accidentally emit
// the retired Phase 2 marker.
const ManifestVersion = "phase3-v1"

// DefaultPathManifest is used only when a configured memory root does not yet
// contain the approved manifest file. Values are deliberately symbolic: the
// request-specific root is resolved by the gateway before file access.
const DefaultPathManifest = `## Memory Context Paths
MEMORY_ROOT       <configured memory root>
CHAT_MEMORY       L0-L4
WIKI              02_Wiki知识库
CODE_GRAPH        03_代码关系图
SKILLS            04_技能库
TEAM_AGENT        05_团队与代理
EVENT_QUEUE       06_事件队列
INDEX             07_索引
RUNTIME           90_运行数据

## Dynamic Fields
user_id, team_id, agent_id, project_id, task_id, conversation_id, turn_id
manifest_version, allowed_asset_ids, retrieval_source_ids, memory_truncated`

// BuildInjectionPackage assembles the protocol compatibility package. The
// gateway's normal LLM path uses inject.Build for database retrieval; this
// entry point remains useful to adapters and older callers that only have the
// binding metadata available.
func BuildInjectionPackage(teamID, agentID, identityCardName string) InjectionPackage {
	identityCard := ""
	identityCardName = sanitizeField(identityCardName)
	teamID = sanitizeField(teamID)
	agentID = sanitizeField(agentID)
	if identityCardName != "" || teamID != "" || agentID != "" {
		identityCard = "## Agent Identity"
		if identityCardName != "" {
			identityCard += fmt.Sprintf("\nRole: %s", identityCardName)
		}
		if teamID != "" {
			identityCard += fmt.Sprintf("\nTeam: %s", teamID)
		}
		if agentID != "" {
			identityCard += fmt.Sprintf("\nAgent: %s", agentID)
		}
	}

	return InjectionPackage{
		PathManifest:    DefaultPathManifest,
		IdentityCard:    identityCard,
		RetrievalSnips:  []string{},
		SourceEventIDs:  []string{},
		ManifestVersion: ManifestVersion,
		Truncated:       false,
	}
}

func sanitizeField(value string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(value))
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

	if len(pkg.RetrievalSnips) > 0 {
		parts = append(parts, "\n## Retrieved Context")
		parts = append(parts, pkg.RetrievalSnips...)
	}

	if pkg.ManifestVersion == "" {
		pkg.ManifestVersion = ManifestVersion
	}
	parts = append(parts, fmt.Sprintf("\n<!-- source_ids: [%s] -->", strings.Join(pkg.SourceEventIDs, ",")))
	parts = append(parts, fmt.Sprintf("<!-- manifest_version: %s -->", pkg.ManifestVersion))
	if pkg.Truncated {
		parts = append(parts, "<!-- memory_truncated: true -->")
	}

	return strings.Join(parts, "\n")
}
