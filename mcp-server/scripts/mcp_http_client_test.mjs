/**
 * Phase 5 HTTP MCP protocol test — connects to the running MCP Server (:8097)
 * through the Streamable HTTP transport, lists the 8 tools and calls them.
 * Also verifies the bearer auth rejects a missing token.
 */
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const KEY = "gw_mcp_test_key_123";
const TEAM = "11111111-2222-3333-4444-555555555555";
const MCP_URL = "http://127.0.0.1:8097/mcp";

let passed = 0;
let failed = 0;
function check(name, cond, detail) {
  if (cond) {
    passed++;
    console.log(`PASS ${name}`);
  } else {
    failed++;
    console.log(`FAIL ${name} :: ${detail}`);
  }
}

// --- auth rejection without bearer ---
{
  const response = await fetch("http://127.0.0.1:8097/mcp", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: {} }),
  });
  check("http /mcp 401 without bearer", response.status === 401, String(response.status));
}

// --- full MCP session with bearer ---
const transport = new StreamableHTTPClientTransport(new URL(MCP_URL), {
  requestInit: {
    headers: { Authorization: `Bearer ${KEY}` },
  },
});
const client = new Client({ name: "phase5-test-client", version: "1.0.0" });
await client.connect(transport);

const { tools } = await client.listTools();
check("tools/list returns 8 tools", Array.isArray(tools) && tools.length === 8, JSON.stringify(tools.map((t) => t.name)));
const names = tools.map((t) => t.name).sort();
check(
  "tools/list names match contract",
  JSON.stringify(names) === JSON.stringify(["codegraph_impact", "memory_append", "memory_get", "memory_search", "session_binding_get", "skill_search", "team_assets_list", "wiki_search"]),
  JSON.stringify(names),
);

const unique = Date.now();
const appended = await client.callTool({ name: "memory_append", arguments: { team_id: TEAM, content: `MCP HTTP 客户端联调 (${unique})：Streamable HTTP 会话可调用 memory_append。`, confidence: 0.8, visibility: "team" } });
check("tool memory_append via HTTP", appended.isError !== true, JSON.stringify(appended.content?.[0]?.text));
const appendData = JSON.parse(appended.content[0].text);
check("tool memory_append asset_type=l1", appendData.asset_type === "l1" && appendData.status === "candidate", JSON.stringify(appendData));

const search = await client.callTool({ name: "memory_search", arguments: { query: "HTTP 客户端", team_id: TEAM, limit: 5, layer: "l1" } });
check("tool memory_search via HTTP", search.isError !== true, JSON.stringify(search.content?.[0]?.text));
const searchData = JSON.parse(search.content[0].text);
check("tool memory_search found appended", Array.isArray(searchData.results) && searchData.results.length >= 1, JSON.stringify(searchData));

const wiki = await client.callTool({ name: "wiki_search", arguments: { query: "并发", team_id: TEAM, limit: 5 } });
check("tool wiki_search via HTTP", wiki.isError !== true && JSON.parse(wiki.content[0].text).results.length >= 1, JSON.stringify(wiki.content?.[0]?.text));

const impact = await client.callTool({ name: "codegraph_impact", arguments: { symbol: "main", repo_id: "repo-test-gateway", depth: 2, team_id: TEAM } });
check("tool codegraph_impact via HTTP", impact.isError !== true && JSON.parse(impact.content[0].text).root.symbol === "main", JSON.stringify(impact.content?.[0]?.text));

const skill = await client.callTool({ name: "skill_search", arguments: { query: "git", team_id: TEAM } });
check("tool skill_search via HTTP", skill.isError !== true && JSON.parse(skill.content[0].text).results.length >= 1, JSON.stringify(skill.content?.[0]?.text));

const binding = await client.callTool({ name: "session_binding_get", arguments: { conversation_id: "conv-mcp-test" } });
check("tool session_binding_get via HTTP", binding.isError !== true && JSON.parse(binding.content[0].text).binding_state === "bound", JSON.stringify(binding.content?.[0]?.text));

const assets = await client.callTool({ name: "team_assets_list", arguments: { team_id: TEAM, asset_type: "skill" } });
check("tool team_assets_list via HTTP", assets.isError !== true && JSON.parse(assets.content[0].text).assets.length >= 1, JSON.stringify(assets.content?.[0]?.text));

const getRes = await client.callTool({ name: "memory_get", arguments: { asset_id: appendData.asset_id, team_id: TEAM } });
check("tool memory_get via HTTP", getRes.isError !== true && JSON.parse(getRes.content[0].text).asset_id === appendData.asset_id, JSON.stringify(getRes.content?.[0]?.text));

await client.close();
console.log(`\n=== ${passed} passed, ${failed} failed ===`);
process.exit(failed === 0 ? 0 : 1);
