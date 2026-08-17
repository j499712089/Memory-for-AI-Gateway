import { DatabaseSync } from "node:sqlite";

const TEAM = "11111111-2222-3333-4444-555555555555";
const db = new DatabaseSync(`F:/memory_plus/teams/${TEAM}/memory.db`);
const row = db
  .prepare("SELECT id FROM assets WHERE asset_type='l1' AND (body_path IS NULL OR body_path='') LIMIT 1")
  .get();
if (!row) {
  console.log("no l1 asset without body_path");
  process.exit(0);
}
db.prepare("UPDATE assets SET body_path='L1_任务纪要/原子事实/phase5-mcp-server-live.md' WHERE id=?").run(row.id);
console.log("set body_path on asset", row.id);

const response = await fetch("http://127.0.0.1:8096/api/mcp/memory/get", {
  method: "POST",
  headers: { "Content-Type": "application/json", Authorization: "Bearer gw_mcp_test_key_123" },
  body: JSON.stringify({ asset_id: row.id, team_id: TEAM }),
});
const data = await response.json();
console.log("status:", response.status);
console.log("body_path:", data.body_path);
console.log("body contains vault L1 markdown:", typeof data.body === "string" && data.body.includes("MCP Server 已通过 8 工具联调"));
console.log("body snippet:", String(data.body).slice(0, 80));
