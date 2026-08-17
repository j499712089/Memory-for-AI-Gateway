/**
 * Phase 5 stdio MCP protocol test — spawns `node dist/index.js --transport
 * stdio`, connects through the StdioClientTransport, lists the 8 tools and
 * calls memory_search + codegraph_impact over stdio.
 */
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";

const KEY = "gw_mcp_test_key_123";
const TEAM = "11111111-2222-3333-4444-555555555555";

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

const transport = new StdioClientTransport({
  command: process.execPath,
  args: ["dist/index.js", "--transport", "stdio"],
  env: { ...process.env, GATEWAY_API_KEY: KEY },
  stderr: "inherit",
});
const client = new Client({ name: "phase5-stdio-test-client", version: "1.0.0" });
await client.connect(transport);

const { tools } = await client.listTools();
check("stdio tools/list 8 tools", Array.isArray(tools) && tools.length === 8, JSON.stringify(tools?.map((t) => t.name)));

const search = await client.callTool({ name: "memory_search", arguments: { query: "事务层", team_id: TEAM, limit: 5 } });
check("stdio memory_search call", search.isError !== true, JSON.stringify(search.content?.[0]?.text));
const searchData = JSON.parse(search.content[0].text);
check("stdio memory_search results", Array.isArray(searchData.results) && searchData.results.length >= 1, JSON.stringify(searchData));

const impact = await client.callTool({ name: "codegraph_impact", arguments: { symbol: "SetupRouter", repo_id: "repo-test-gateway", depth: 1, team_id: TEAM } });
check("stdio codegraph_impact call", impact.isError !== true, JSON.stringify(impact.content?.[0]?.text));
const impactData = JSON.parse(impact.content[0].text);
check("stdio codegraph_impact callees", Array.isArray(impactData.callees) && impactData.callees.length >= 1, JSON.stringify(impactData));

await client.close();
console.log(`\n=== ${passed} passed, ${failed} failed ===`);
process.exit(failed === 0 ? 0 : 1);
