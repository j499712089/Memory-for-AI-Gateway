/**
 * GatewayClient — the single access path from the MCP Server to the Memory
 * Gateway backend (:8096). The MCP Server NEVER touches SQLite directly: every
 * read goes through the gateway's retrieval/query layer and the only write
 * (memory/append) goes through the same SQLite transaction layer as the
 * Worker pipeline.
 *
 * The gateway bearer key is taken from GATEWAY_API_KEY (never persisted).
 */

export interface MemorySearchResultItem {
  asset_id: string;
  asset_type: string;
  layer: string;
  summary: string;
  visibility: string;
  source_event_ids: string[];
  score: number;
  version: number;
  snippet: string;
}

export interface MemorySearchResponse {
  results: MemorySearchResultItem[];
  truncated: boolean;
}

export interface MemoryGetResponse {
  asset_id: string;
  asset_type: string;
  name: string;
  summary: string;
  body: string;
  body_path: string;
  source_event_ids: string[];
  confidence: number;
  visibility: string;
  version: number;
  status: string;
  created_at: string;
}

export interface MemoryAppendResponse {
  asset_id: string;
  asset_type: string;
  version: number;
  status: string;
  created_at: string;
}

export interface WikiSearchResponse {
  results: {
    page_id: string;
    title: string;
    slug: string;
    snippet: string;
    score: number;
    links: string[];
  }[];
}

export interface CodeGraphImpactResponse {
  root: { symbol: string; file: string; kind: string };
  callers: { symbol: string; file: string; depth: number }[];
  callees: { symbol: string; file: string; depth: number }[];
  affected_files: string[];
}

export interface SkillSearchResponse {
  results: {
    skill_id: string;
    name: string;
    display_name: string;
    version: string;
    status: string;
    trigger_boundary: string;
    steps_summary: string[];
    source_ids: string[];
  }[];
}

export interface BindingGetResponse {
  session_id: string;
  conversation_id: string;
  team_id: string;
  agent_id: string;
  identity_card_id: string;
  upstream_channel_id: string;
  binding_version: number;
  binding_state: string;
}

export interface TeamAssetsListResponse {
  assets: {
    asset_id: string;
    asset_type: string;
    name: string;
    summary: string;
    visibility: string;
    status: string;
    version: number;
    updated_at: string;
  }[];
}

export class GatewayError extends Error {
  constructor(
    public readonly status: number,
    public readonly errorType: string,
    message: string,
  ) {
    super(message);
    this.name = "GatewayError";
  }
}

export interface GatewayClientOptions {
  baseUrl?: string;
  apiKey?: string;
  timeoutMs?: number;
}

export class GatewayClient {
  private readonly baseUrl: string;
  private readonly apiKey: string;
  private readonly timeoutMs: number;

  constructor(options: GatewayClientOptions = {}) {
    this.baseUrl = (options.baseUrl ?? process.env.GATEWAY_BASE_URL ?? "http://127.0.0.1:8096").replace(/\/+$/, "");
    this.apiKey = options.apiKey ?? process.env.GATEWAY_API_KEY ?? "";
    this.timeoutMs = options.timeoutMs ?? 10_000;
  }

  private async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    if (!this.apiKey) {
      throw new GatewayError(401, "unauthorized", "GATEWAY_API_KEY is not configured");
    }
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const response = await fetch(`${this.baseUrl}${path}`, {
        method,
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${this.apiKey}`,
        },
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: controller.signal,
      });
      const text = await response.text();
      let payload: unknown = null;
      if (text) {
        try {
          payload = JSON.parse(text);
        } catch {
          payload = text;
        }
      }
      if (!response.ok) {
        const errorBody = (payload as { error?: { type?: string; message?: string } })?.error;
        throw new GatewayError(
          response.status,
          errorBody?.type ?? "gateway_error",
          errorBody?.message ?? `gateway ${method} ${path} returned ${response.status}`,
        );
      }
      return payload as T;
    } catch (error) {
      if (error instanceof GatewayError) {
        throw error;
      }
      throw new GatewayError(502, "gateway_unreachable", `gateway request failed: ${(error as Error).message}`);
    } finally {
      clearTimeout(timer);
    }
  }

  async probeHealth(): Promise<{ ok: boolean; detail: string }> {
    try {
      const controller = new AbortController();
      const timer = setTimeout(() => controller.abort(), 2000);
      const response = await fetch(`${this.baseUrl}/health`, { signal: controller.signal });
      clearTimeout(timer);
      if (!response.ok) {
        return { ok: false, detail: `gateway /health -> ${response.status}` };
      }
      return { ok: true, detail: `gateway /health -> ${response.status}` };
    } catch (error) {
      return { ok: false, detail: `gateway unreachable: ${(error as Error).message}` };
    }
  }

  memorySearch(args: { query: string; team_id?: string; identity_card_id?: string; limit?: number; layer?: string }): Promise<MemorySearchResponse> {
    return this.request<MemorySearchResponse>("POST", "/api/mcp/memory/search", args);
  }

  memoryGet(args: { asset_id: string; team_id?: string }): Promise<MemoryGetResponse> {
    return this.request<MemoryGetResponse>("POST", "/api/mcp/memory/get", args);
  }

  memoryAppend(args: { team_id?: string; content: string; source_ids?: string[]; confidence?: number; visibility?: string }): Promise<MemoryAppendResponse> {
    return this.request<MemoryAppendResponse>("POST", "/api/mcp/memory/append", args);
  }

  wikiSearch(args: { query: string; team_id?: string; limit?: number }): Promise<WikiSearchResponse> {
    return this.request<WikiSearchResponse>("POST", "/api/mcp/wiki/search", args);
  }

  codeGraphImpact(args: { symbol?: string; file_path?: string; repo_id: string; depth?: number; team_id?: string }): Promise<CodeGraphImpactResponse> {
    return this.request<CodeGraphImpactResponse>("POST", "/api/mcp/codegraph/impact", args);
  }

  skillSearch(args: { query?: string; status?: string; version?: string; team_id?: string }): Promise<SkillSearchResponse> {
    return this.request<SkillSearchResponse>("POST", "/api/mcp/skill/search", args);
  }

  bindingGet(args: { session_id?: string; conversation_id?: string }): Promise<BindingGetResponse> {
    return this.request<BindingGetResponse>("POST", "/api/mcp/binding/get", args);
  }

  teamAssetsList(args: { team_id?: string; asset_type?: string; visibility?: string; status?: string; limit?: number; offset?: number }): Promise<TeamAssetsListResponse> {
    return this.request<TeamAssetsListResponse>("POST", "/api/mcp/assets/list", args);
  }
}
