/**
 * Phase 5 live integration test — calls all 8 /api/mcp/* endpoints against the
 * running Gateway (:8096) and asserts contract shape. Test-only script.
 */
const KEY = "gw_mcp_test_key_123";
const TEAM = "11111111-2222-3333-4444-555555555555";
const BASE = "http://127.0.0.1:8096";

async function call(path, body) {
  const response = await fetch(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${KEY}` },
    body: JSON.stringify(body),
  });
  const text = await response.text();
  let data;
  try {
    data = JSON.parse(text);
  } catch {
    data = text;
  }
  return { status: response.status, data };
}

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

// 3. memory/append (write path) — run first so search/get have a visible asset
{
  const unique = Date.now();
  const content = `MCP Server Phase 5 联调 (${unique})：memory_append 走 SQLite 事务层落库，不直接写 Markdown。`;
  const r = await call("/api/mcp/memory/append", {
    team_id: TEAM,
    content,
    source_ids: ["evt-51"],
    confidence: 0.85,
    visibility: "team",
  });
  check("memory/append 201", r.status === 201, JSON.stringify(r.data));
  check("memory/append response", r.data?.asset_type === "l1" && r.data?.status === "candidate" && r.data?.asset_id, JSON.stringify(r.data));
  globalThis.__appendedAsset = r.data?.asset_id;
  // duplicate append -> conflict (dedup by content-derived slug)
  const dup = await call("/api/mcp/memory/append", {
    team_id: TEAM,
    content,
    source_ids: ["evt-51"],
    confidence: 0.85,
    visibility: "team",
  });
  check("memory/append dedup conflict", dup.status === 409, JSON.stringify(dup.data));
}

// 1. memory/search
{
  const r = await call("/api/mcp/memory/search", { query: "事务层", team_id: TEAM, limit: 5, layer: "l1" });
  check("memory/search 200", r.status === 200, JSON.stringify(r.data));
  check("memory/search results shape", Array.isArray(r.data?.results) && typeof r.data.truncated === "boolean", JSON.stringify(r.data));
  const item = r.data?.results?.[0];
  check("memory/search item fields", item && ["asset_id", "asset_type", "layer", "summary", "visibility", "source_event_ids", "score", "version", "snippet"].every((k) => k in item), JSON.stringify(item));
}

// 2. memory/get
{
  const search = await call("/api/mcp/memory/search", { query: "事务层", team_id: TEAM, limit: 1 });
  const assetId = search.data?.results?.[0]?.asset_id;
  const r = await call("/api/mcp/memory/get", { asset_id: assetId, team_id: TEAM });
  check("memory/get 200", r.status === 200, JSON.stringify(r.data));
  check("memory/get body fields", r.data?.asset_id === assetId && "body" in r.data && "body_path" in r.data && "confidence" in r.data, JSON.stringify(r.data));
  const missing = await call("/api/mcp/memory/get", { asset_id: "no-such-asset", team_id: TEAM });
  check("memory/get 404 on missing", missing.status === 404 && missing.data?.error?.type === "not_found", JSON.stringify(missing.data));
}

// 4. wiki/search
{
  const r = await call("/api/mcp/wiki/search", { query: "并发", team_id: TEAM, limit: 5 });
  check("wiki/search 200", r.status === 200, JSON.stringify(r.data));
  check("wiki/search results", Array.isArray(r.data?.results) && r.data.results.length >= 1, JSON.stringify(r.data));
  const page = r.data?.results?.[0];
  check("wiki/search page fields", page && ["page_id", "title", "slug", "snippet", "score", "links"].every((k) => k in page), JSON.stringify(page));
}

// 5. codegraph/impact
{
  const r = await call("/api/mcp/codegraph/impact", { symbol: "main", repo_id: "repo-test-gateway", depth: 2, team_id: TEAM });
  check("codegraph/impact 200", r.status === 200, JSON.stringify(r.data));
  check("codegraph/impact root", r.data?.root?.symbol === "main" && r.data?.root?.file === "cmd/gateway/main.go", JSON.stringify(r.data?.root));
  check("codegraph/impact callers/callees", Array.isArray(r.data?.callers) && Array.isArray(r.data?.callees), JSON.stringify(r.data));
  check("codegraph/impact affected_files includes router", Array.isArray(r.data?.affected_files) && r.data.affected_files.includes("internal/httpx/router.go"), JSON.stringify(r.data?.affected_files));
  const shallow = await call("/api/mcp/codegraph/impact", { symbol: "SetupRouter", repo_id: "repo-test-gateway", depth: 1, team_id: TEAM });
  check("codegraph/impact depth=1", shallow.status === 200 && shallow.data?.callees?.length >= 1, JSON.stringify(shallow.data));
}

// 6. skill/search
{
  const r = await call("/api/mcp/skill/search", { query: "git", status: "approved", team_id: TEAM });
  check("skill/search 200", r.status === 200, JSON.stringify(r.data));
  const skill = r.data?.results?.[0];
  check("skill/search fields", skill && skill.name === "git-batch-commit" && skill.version === "1.2.0" && Array.isArray(skill.steps_summary), JSON.stringify(skill));
}

// 7. binding/get
{
  const r = await call("/api/mcp/binding/get", { conversation_id: "conv-mcp-test" });
  check("binding/get 200", r.status === 200, JSON.stringify(r.data));
  check("binding/get fields", r.data?.conversation_id === "conv-mcp-test" && r.data?.binding_state === "bound" && r.data?.team_id === TEAM, JSON.stringify(r.data));
}

// 8. assets/list
{
  const r = await call("/api/mcp/assets/list", { team_id: TEAM, asset_type: "skill", visibility: "team" });
  check("assets/list 200", r.status === 200, JSON.stringify(r.data));
  check("assets/list items", Array.isArray(r.data?.assets) && r.data.assets.some((a) => a.asset_type === "skill"), JSON.stringify(r.data?.assets));
  const all = await call("/api/mcp/assets/list", { team_id: TEAM });
  check("assets/list no filter", all.status === 200 && all.data?.assets?.length >= 4, JSON.stringify(all.data?.assets?.length));
}

// ACL: wrong team -> forbidden
{
  const r = await call("/api/mcp/memory/search", { query: "WAL", team_id: "99999999-0000-0000-0000-000000000000" });
  check("ACL wrong team forbidden", r.status === 403, JSON.stringify(r.data));
}

// scope: key without mcp scope -> forbidden
{
  const crypto = await import("node:crypto");
  const { DatabaseSync } = await import("node:sqlite");
  const plain = "gw_no_mcp_scope_key";
  const hash = crypto.createHash("sha256").update(plain).digest("hex");
  const gdb = new DatabaseSync("F:/memory_plus/.runtime/memory-gateway.db");
  const now = new Date().toISOString().replace(/\.\d+Z$/, "Z");
  gdb.prepare("INSERT OR REPLACE INTO api_keys (id,team_id,key_hash,key_ref,scopes,enabled,created_at) VALUES (?,?,?,?,?,1,?)").run(
    "99999999-aaaa-bbbb-cccc-dddddddddddd", TEAM, hash, "test:noscope", '["gateway"]', now,
  );
  const response = await fetch(BASE + "/api/mcp/memory/search", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${plain}` },
    body: JSON.stringify({ query: "事务层", team_id: TEAM }),
  });
  const data = await response.json();
  check("scope check 403 without mcp scope", response.status === 403 && data?.error?.type === "forbidden", JSON.stringify(data));
}

console.log(`\n=== ${passed} passed, ${failed} failed ===`);
process.exit(failed === 0 ? 0 : 1);
