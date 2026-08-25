import type { McpToolDef } from "./types.js";
import { memorySearchTool } from "./memory_search.js";
import { memoryGetTool } from "./memory_get.js";
import { memoryAppendTool } from "./memory_append.js";
import { wikiSearchTool } from "./wiki_search.js";
import { codeGraphImpactTool } from "./codegraph_impact.js";
import { skillSearchTool } from "./skill_search.js";
import { sessionBindingGetTool } from "./session_binding_get.js";
import { teamAssetsListTool } from "./team_assets_list.js";

/** The 8 MCP tools, one per api-contract §4 endpoint. */
export const tools: McpToolDef[] = [
  memorySearchTool,
  memoryGetTool,
  memoryAppendTool,
  wikiSearchTool,
  codeGraphImpactTool,
  skillSearchTool,
  sessionBindingGetTool,
  teamAssetsListTool,
];
