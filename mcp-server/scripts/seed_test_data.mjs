/**
 * Test-only seeding script for the Phase 5 integration test.
 * Seeds wiki page, codegraph repo/symbols/edges, skill, and a session binding
 * into the test team DB. NOT part of the MCP server runtime.
 */
import { DatabaseSync } from "node:sqlite";

const TEAM = "11111111-2222-3333-4444-555555555555";
const db = new DatabaseSync(`F:/memory_plus/teams/${TEAM}/memory.db`);
const now = new Date().toISOString().replace(/\.\d+Z$/, "Z");

// --- backing assets (FK targets for wiki / codegraph / skill) ---
const assetRows = [
  ["aaa00000-0000-0000-0000-000000000001", "wiki", "SQLite 并发写入", "sqlite-concurrency", "ready"],
  ["aaa00000-0000-0000-0000-000000000002", "wiki", "本地部署", "local-deploy", "ready"],
  ["aaa00000-0000-0000-0000-000000000003", "codegraph", "gateway 仓库", "gateway-repo", "ready"],
  ["aaa00000-0000-0000-0000-000000000004", "skill", "git-batch-commit", "git-batch-commit", "approved"],
];
for (const [id, type, name, slug, status] of assetRows) {
  db.prepare(
    `INSERT OR IGNORE INTO assets (id, team_id, asset_type, name, slug, summary, status, visibility, version, source_event_ids, confidence, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, 'team', 1, '[]', 0.9, ?, ?)`,
  ).run(id, TEAM, type, name, slug, name, status, now, now);
}

// --- wiki page + FTS ---
const wikiId = "wiki-sqlite-concurrency";
db.prepare(
  `INSERT OR REPLACE INTO wiki_pages (id, asset_id, title, slug, content_md, frontmatter_json, status, built_at, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
).run(
  wikiId,
  "aaa00000-0000-0000-0000-000000000001",
  "SQLite 并发写入",
  "sqlite-concurrency",
  "# SQLite 并发写入\n\nWAL 模式配合 BEGIN IMMEDIATE 短事务与 busy_timeout=5000 是并发写保护的关键。",
  '{"tags":["sqlite","concurrency"]}',
  "ready",
  now,
  now,
  now,
);
db.prepare("DELETE FROM wiki_fts WHERE page_id = ?").run(wikiId);
db.prepare("INSERT INTO wiki_fts (page_id, title, content) VALUES (?, ?, ?)").run(
  wikiId,
  "SQLite 并发写入",
  "# SQLite 并发写入\n\nWAL 模式配合 BEGIN IMMEDIATE 短事务与 busy_timeout=5000 是并发写保护的关键。",
);
const wiki2Id = "wiki-deploy";
db.prepare(
  `INSERT OR REPLACE INTO wiki_pages (id, asset_id, title, slug, content_md, frontmatter_json, status, built_at, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
).run(
  wiki2Id,
  "aaa00000-0000-0000-0000-000000000002",
  "本地部署",
  "local-deploy",
  "# 本地部署\n\nGateway :8096、MCP Server :8097、Web Panel :8125 全部绑定 127.0.0.1。",
  "{}",
  "ready",
  now,
  now,
  now,
);
db.prepare("DELETE FROM wiki_fts WHERE page_id = ?").run(wiki2Id);
db.prepare("INSERT INTO wiki_fts (page_id, title, content) VALUES (?, ?, ?)").run(
  wiki2Id,
  "本地部署",
  "# 本地部署\n\nGateway :8096、MCP Server :8097、Web Panel :8125 全部绑定 127.0.0.1。",
);
// wiki edge: deploy links to concurrency
db.prepare("INSERT OR IGNORE INTO wiki_edges (id, from_page_id, to_page_id, link_type) VALUES (?, ?, ?, 'internal')").run(
  "edge-" + wiki2Id + "-" + wikiId,
  wiki2Id,
  wikiId,
);

// --- codegraph: repo + files + symbols + edges ---
const repoId = "repo-test-gateway";
db.prepare(
  `INSERT OR REPLACE INTO code_repos (id, asset_id, repo_url, local_path, branch, head_commit, status, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
).run(repoId, "aaa00000-0000-0000-0000-000000000003", "https://github.com/multica-ai/multica", "F:/memory_plus/gateway", "main", "c8e3bbe", "ready", now, now);

const fileMain = "file-repo-main";
db.prepare("INSERT OR IGNORE INTO code_files (id, repo_id, path, file_hash, language, size_bytes, indexed_at) VALUES (?, ?, ?, ?, ?, ?, ?)").run(
  fileMain, repoId, "cmd/gateway/main.go", "hash-main", "go", 1000, now,
);
const fileRouter = "file-repo-router";
db.prepare("INSERT OR IGNORE INTO code_files (id, repo_id, path, file_hash, language, size_bytes, indexed_at) VALUES (?, ?, ?, ?, ?, ?, ?)").run(
  fileRouter, repoId, "internal/httpx/router.go", "hash-router", "go", 2000, now,
);
const fileMcp = "file-repo-mcp";
db.prepare("INSERT OR IGNORE INTO code_files (id, repo_id, path, file_hash, language, size_bytes, indexed_at) VALUES (?, ?, ?, ?, ?, ?, ?)").run(
  fileMcp, repoId, "internal/httpx/mcp_handler.go", "hash-mcp", "go", 3000, now,
);

const symMain = "sym-main";
const symRouter = "sym-router";
const symMcp = "sym-mcp";
db.prepare("INSERT OR IGNORE INTO code_symbols (id, file_id, name, kind, signature, line_start, line_end, symbol_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?)").run(
  symMain, fileMain, "main", "function", "func main()", 15, 40, "h-main",
);
db.prepare("INSERT OR IGNORE INTO code_symbols (id, file_id, name, kind, signature, line_start, line_end, symbol_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?)").run(
  symRouter, fileRouter, "SetupRouter", "function", "func SetupRouter()", 10, 60, "h-router",
);
db.prepare("INSERT OR IGNORE INTO code_symbols (id, file_id, name, kind, signature, line_start, line_end, symbol_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?)").run(
  symMcp, fileMcp, "HandleMemorySearch", "method", "func (h *MCPHandler) HandleMemorySearch", 30, 70, "h-mcp",
);
// main calls SetupRouter; SetupRouter calls HandleMemorySearch
db.prepare("INSERT OR IGNORE INTO code_edges (id, source_symbol_id, target_symbol_id, edge_type) VALUES (?, ?, ?, 'call')").run(
  "edge-main-router", symMain, symRouter,
);
db.prepare("INSERT OR IGNORE INTO code_edges (id, source_symbol_id, target_symbol_id, edge_type) VALUES (?, ?, ?, 'call')").run(
  "edge-router-mcp", symRouter, symMcp,
);

// --- skill ---
const skillAssetId = "aaa00000-0000-0000-0000-000000000004";
db.prepare(
  `INSERT OR REPLACE INTO skills (id, asset_id, name, display_name, version, status, scope, trigger_boundary, steps_json, validation_json, source_ids, resource_refs, entrypoint, manifest_path, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
).run(
  "skill-git-batch-commit", skillAssetId, "git-batch-commit", "Git 批量提交", "1.2.0", "approved", "team",
  "当 L1-L4 资产晋升或每 10 分钟时触发",
  '["git add -A", "git commit -m \\"memory: batch\\"", "绝不 push"]',
  '{"requires_git": true}',
  '["evt-9"]',
  "[]",
  "git_commit.js",
  "04_技能库/git-batch-commit/1.2.0/SKILL.md",
  now,
  now,
);
db.prepare(
  `INSERT OR REPLACE INTO skill_versions (id, skill_id, version, status, content_ref, source_ids, created_by, created_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
).run("sv-git-1", "skill-git-batch-commit", "1.2.0", "approved", "04_技能库/git-batch-commit/1.2.0/SKILL.md", '["evt-9"]', "worker", now);

// --- session binding (global DB) ---
const gdb = new DatabaseSync("F:/memory_plus/.runtime/memory-gateway.db");
const sessionId = "33333333-4444-5555-6666-777777777777";
gdb.prepare(
  `INSERT OR IGNORE INTO sessions (id, team_id, kind, conversation_id, binding_version, status, created_at, updated_at)
   VALUES (?, ?, 'main', ?, 1, 'active', ?, ?)`,
).run(sessionId, TEAM, "conv-mcp-test", now, now);
gdb.prepare(
  `INSERT OR REPLACE INTO session_bindings (id, session_id, conversation_id, team_id, agent_id, identity_card_id, upstream_channel_id, binding_version, binding_state, binding_source, last_resolved_at, created_at, updated_at)
   VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
).run(
  "binding-mcp-test", sessionId, "conv-mcp-test", TEAM, null, null, null, 1, "bound", "service", now, now, now,
);

console.log("seeded: wiki(2 pages + 1 edge), codegraph(1 repo, 3 files, 3 symbols, 2 call edges), skill(1), binding(1)");
