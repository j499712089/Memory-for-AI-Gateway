import { describe, it, expect, vi, type Mock } from "vitest";
import { GatewayClient } from "../src/gateway_client.js";
import { tools } from "../src/tools/index.js";
import { memorySearchTool } from "../src/tools/memory_search.js";
import { memoryGetTool } from "../src/tools/memory_get.js";
import { memoryAppendTool } from "../src/tools/memory_append.js";
import { wikiSearchTool } from "../src/tools/wiki_search.js";
import { codeGraphImpactTool } from "../src/tools/codegraph_impact.js";
import { skillSearchTool } from "../src/tools/skill_search.js";
import { sessionBindingGetTool } from "../src/tools/session_binding_get.js";
import { teamAssetsListTool } from "../src/tools/team_assets_list.js";

function createMockClient(): GatewayClient {
  return {
    memorySearch: vi.fn().mockResolvedValue({ results: [], truncated: false }),
    memoryGet: vi.fn().mockResolvedValue({
      asset_id: "a1", asset_type: "l2", name: "n", summary: "s", body: "b", body_path: "L2/1.md",
      source_event_ids: [], confidence: 0.9, visibility: "team", version: 1, status: "approved", created_at: "t",
    }),
    memoryAppend: vi.fn().mockResolvedValue({ asset_id: "a2", asset_type: "l1", version: 1, status: "candidate", created_at: "t" }),
    wikiSearch: vi.fn().mockResolvedValue({ results: [] }),
    codeGraphImpact: vi.fn().mockResolvedValue({ root: { symbol: "main", file: "main.go", kind: "function" }, callers: [], callees: [], affected_files: [] }),
    skillSearch: vi.fn().mockResolvedValue({ results: [] }),
    bindingGet: vi.fn().mockResolvedValue({ session_id: "s1", conversation_id: "c1", team_id: "t1", agent_id: "", identity_card_id: "", upstream_channel_id: "", binding_version: 1, binding_state: "bound" }),
    teamAssetsList: vi.fn().mockResolvedValue({ assets: [] }),
    probeHealth: vi.fn().mockResolvedValue({ ok: true, detail: "gateway /health -> 200" }),
  } as unknown as GatewayClient;
}

function textOf(result: { content: { text: string }[] }): unknown {
  return JSON.parse(result.content[0].text);
}

describe("MCP tool registry", () => {
  it("exposes exactly the 8 contract tools", () => {
    const names = tools.map((tool) => tool.name).sort();
    expect(names).toEqual([
      "codegraph_impact",
      "memory_append",
      "memory_get",
      "memory_search",
      "session_binding_get",
      "skill_search",
      "team_assets_list",
      "wiki_search",
    ]);
  });
});

describe("memory_search", () => {
  it("validates input and calls the gateway client with parsed args", async () => {
    const client = createMockClient();
    const result = await memorySearchTool.call(client, { query: "sqlite 并发", limit: 5, layer: "l2" });
    expect(client.memorySearch).toHaveBeenCalledWith({ query: "sqlite 并发", limit: 5, layer: "l2" });
    expect(textOf(result)).toEqual({ results: [], truncated: false });
  });

  it("rejects input without query", async () => {
    const client = createMockClient();
    await expect(memorySearchTool.call(client, {})).rejects.toThrow();
    expect(client.memorySearch).not.toHaveBeenCalled();
  });
});

describe("memory_get", () => {
  it("passes asset_id through and returns the structured body", async () => {
    const client = createMockClient();
    const result = await memoryGetTool.call(client, { asset_id: "a1", team_id: "t1" });
    expect(client.memoryGet).toHaveBeenCalledWith({ asset_id: "a1", team_id: "t1" });
    const data = textOf(result) as { asset_id: string; body: string; body_path: string };
    expect(data.asset_id).toBe("a1");
    expect(data.body).toBe("b");
    expect(data.body_path).toBe("L2/1.md");
  });

  it("rejects input without asset_id", async () => {
    const client = createMockClient();
    await expect(memoryGetTool.call(client, { team_id: "t1" })).rejects.toThrow();
    expect(client.memoryGet).not.toHaveBeenCalled();
  });
});

describe("memory_append", () => {
  it("applies zod defaults and sends a write through the client", async () => {
    const client = createMockClient();
    const result = await memoryAppendTool.call(client, { content: "原子记忆", source_ids: ["e1"] });
    expect(client.memoryAppend).toHaveBeenCalledWith({
      content: "原子记忆",
      source_ids: ["e1"],
      confidence: 0.8,
    });
    const data = textOf(result) as { asset_type: string; status: string };
    expect(data.asset_type).toBe("l1");
    expect(data.status).toBe("candidate");
  });

  it("rejects empty content and out-of-range confidence", async () => {
    const client = createMockClient();
    await expect(memoryAppendTool.call(client, { content: "" })).rejects.toThrow();
    await expect(memoryAppendTool.call(client, { content: "x", confidence: 2 })).rejects.toThrow();
    expect(client.memoryAppend).not.toHaveBeenCalled();
  });
});

describe("wiki_search", () => {
  it("passes query and limit through", async () => {
    const client = createMockClient();
    await wikiSearchTool.call(client, { query: "部署", limit: 3 });
    expect(client.wikiSearch).toHaveBeenCalledWith({ query: "部署", limit: 3 });
  });

  it("rejects input without query", async () => {
    const client = createMockClient();
    await expect(wikiSearchTool.call(client, { limit: 1 })).rejects.toThrow();
  });
});

describe("codegraph_impact", () => {
  it("passes symbol/repo/depth and defaults depth to 3", async () => {
    const client = createMockClient();
    const result = await codeGraphImpactTool.call(client, { repo_id: "r1", symbol: "Register", file_path: "internal/router.go" });
    expect(client.codeGraphImpact).toHaveBeenCalledWith({
      repo_id: "r1",
      symbol: "Register",
      file_path: "internal/router.go",
      depth: 3,
    });
    const data = textOf(result) as { root: { symbol: string }; affected_files: string[] };
    expect(data.root.symbol).toBe("main");
    expect(Array.isArray(data.affected_files)).toBe(true);
  });

  it("rejects input without repo_id", async () => {
    const client = createMockClient();
    await expect(codeGraphImpactTool.call(client, { symbol: "main" })).rejects.toThrow();
    expect(client.codeGraphImpact).not.toHaveBeenCalled();
  });
});

describe("skill_search", () => {
  it("passes status/version filters through", async () => {
    const client = createMockClient();
    await skillSearchTool.call(client, { query: "git", status: "approved", version: "1.2.0" });
    expect(client.skillSearch).toHaveBeenCalledWith({ query: "git", status: "approved", version: "1.2.0" });
  });

  it("accepts an empty query (list all)", async () => {
    const client = createMockClient();
    await skillSearchTool.call(client, { status: "approved" });
    expect(client.skillSearch).toHaveBeenCalledWith({ status: "approved" });
  });
});

describe("session_binding_get", () => {
  it("accepts conversation_id alone", async () => {
    const client = createMockClient();
    const result = await sessionBindingGetTool.call(client, { conversation_id: "c1" });
    expect(client.bindingGet).toHaveBeenCalledWith({ conversation_id: "c1" });
    const data = textOf(result) as { binding_version: number; binding_state: string };
    expect(data.binding_version).toBe(1);
    expect(data.binding_state).toBe("bound");
  });

  it("rejects input with neither session_id nor conversation_id", async () => {
    const client = createMockClient();
    await expect(sessionBindingGetTool.call(client, {})).rejects.toThrow();
    expect(client.bindingGet).not.toHaveBeenCalled();
  });
});

describe("team_assets_list", () => {
  it("passes asset_type/visibility filters and pagination through", async () => {
    const client = createMockClient();
    await teamAssetsListTool.call(client, { asset_type: "skill", visibility: "team", limit: 5, offset: 0 });
    expect(client.teamAssetsList).toHaveBeenCalledWith({
      asset_type: "skill",
      visibility: "team",
      limit: 5,
      offset: 0,
    });
  });

  it("applies default limit", async () => {
    const client = createMockClient();
    await teamAssetsListTool.call(client, {});
    expect(client.teamAssetsList).toHaveBeenCalledWith({ limit: 20, offset: 0 });
  });
});

describe("gateway_client", () => {
  it("routes each method to the matching /api/mcp path", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => "{}" });
    vi.stubGlobal("fetch", fetchMock);
    const client = new GatewayClient({ baseUrl: "http://127.0.0.1:8096", apiKey: "k", timeoutMs: 1000 });

    await client.memorySearch({ query: "q" });
    await client.memoryGet({ asset_id: "a" });
    await client.memoryAppend({ content: "c" });
    await client.wikiSearch({ query: "q" });
    await client.codeGraphImpact({ repo_id: "r" });
    await client.skillSearch({});
    await client.bindingGet({ conversation_id: "c" });
    await client.teamAssetsList({});

    const calls = fetchMock.mock.calls.map((call) => (call[0] as Request).toString());
    for (const path of [
      "/api/mcp/memory/search",
      "/api/mcp/memory/get",
      "/api/mcp/memory/append",
      "/api/mcp/wiki/search",
      "/api/mcp/codegraph/impact",
      "/api/mcp/skill/search",
      "/api/mcp/binding/get",
      "/api/mcp/assets/list",
    ]) {
      expect(calls.some((url) => url.includes(path))).toBe(true);
    }
    vi.unstubAllGlobals();
  });

  it("throws GatewayError with a typed error body", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 403,
        text: async () => JSON.stringify({ error: { type: "forbidden", message: "no mcp scope" } }),
      }),
    );
    const client = new GatewayClient({ baseUrl: "http://127.0.0.1:8096", apiKey: "k", timeoutMs: 1000 });
    await expect(client.memorySearch({ query: "q" })).rejects.toMatchObject({ status: 403, errorType: "forbidden" });
    vi.unstubAllGlobals();
  });
});
