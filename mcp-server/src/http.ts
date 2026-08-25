import express, { type Express, type Request, type Response } from "express";
import { randomUUID } from "node:crypto";
import { createMcpExpressApp } from "@modelcontextprotocol/sdk/server/express.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { GatewayClient } from "./gateway_client.js";
import { createMcpServer, SERVER_VERSION } from "./server.js";
import { createAuthMiddleware } from "./auth.js";

export interface McpHttpOptions {
  gatewayClient: GatewayClient;
  version?: string;
}

const OBSIDIAN_BASE_URL = "http://127.0.0.1:27124";

async function probeObsidian(): Promise<string> {
  try {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 2000);
    const response = await fetch(`${OBSIDIAN_BASE_URL}/`, { signal: controller.signal });
    clearTimeout(timer);
    return response.ok ? "ok" : `unavailable (${response.status})`;
  } catch (error) {
    return `unavailable (${(error as Error).message})`;
  }
}

interface McpSession {
  transport: StreamableHTTPServerTransport;
  server: McpServer;
}

/**
 * Creates the Express app that hosts the Streamable HTTP MCP endpoint and the
 * /health probe on :8097. Bearer auth is enforced by createAuthMiddleware on
 * every path except /health.
 *
 * The SDK Protocol (McpServer) supports one connected transport per instance,
 * so each Streamable HTTP session gets its own McpServer with the same 8
 * tools registered (see server.ts). Sessions are keyed by their session id.
 */
export function createMcpHttpApp(options: McpHttpOptions): Express {
  const { gatewayClient, version = SERVER_VERSION } = options;
  const app = createMcpExpressApp({ host: "127.0.0.1" });
  app.use(createAuthMiddleware());

  const sessions = new Map<string, McpSession>();

  const sessionOf = (req: Request): string | undefined => {
    const value = req.headers["mcp-session-id"];
    return Array.isArray(value) ? value[0] : value;
  };

  app.post("/mcp", async (req: Request, res: Response) => {
    const sessionId = sessionOf(req);
    let session = sessionId ? sessions.get(sessionId) : undefined;
    if (!session) {
      const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: () => randomUUID() });
      const server = createMcpServer(gatewayClient);
      try {
        await server.connect(transport);
      } catch (error) {
        res.status(500).json({ error: { type: "internal_error", message: (error as Error).message } });
        return;
      }
      session = { transport, server };
    }
    try {
      await session.transport.handleRequest(req, res, req.body);
    } catch (error) {
      if (!res.headersSent) {
        res.status(500).json({ error: { type: "internal_error", message: (error as Error).message } });
      }
      return;
    }
    // Register the session under its id once the first request (initialize)
    // has generated it, so subsequent requests can resume it.
    const generatedId = session.transport.sessionId;
    if (generatedId && !sessions.has(generatedId)) {
      sessions.set(generatedId, session);
    }
  });

  app.get("/mcp", async (req: Request, res: Response) => {
    const sessionId = sessionOf(req);
    const session = sessionId ? sessions.get(sessionId) : undefined;
    if (!session) {
      res.status(404).json({ error: { type: "not_found", message: "MCP session not found" } });
      return;
    }
    await session.transport.handleRequest(req, res, req.body);
  });

  app.delete("/mcp", async (req: Request, res: Response) => {
    const sessionId = sessionOf(req);
    if (!sessionId) {
      res.status(404).json({ error: { type: "not_found", message: "MCP session not found" } });
      return;
    }
    const session = sessions.get(sessionId);
    if (!session) {
      res.status(404).json({ error: { type: "not_found", message: "MCP session not found" } });
      return;
    }
    await session.server.close();
    await session.transport.close();
    sessions.delete(sessionId);
    res.status(200).json({ ok: true });
  });

  app.get("/health", async (_req: Request, res: Response) => {
    const gateway = await gatewayClient.probeHealth();
    const obsidian = await probeObsidian();
    const status = gateway.ok ? "ok" : "degraded";
    res.json({
      status,
      version,
      transport: "http",
      gateway: gateway.ok ? "ok" : gateway.detail,
      obsidian,
    });
  });

  return app;
}
