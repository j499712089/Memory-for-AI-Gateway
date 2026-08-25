/**
 * HTTP bearer authentication for the Streamable HTTP transport (:8097).
 *
 * The SDK transport does NOT do business auth (per api-contract §5): the MCP
 * client's bearer key is validated here against the Gateway's own key store by
 * calling an authenticated Gateway endpoint. A key is accepted only when the
 * Gateway accepts it (which includes the `mcp` scope check done on the
 * `/api/mcp/*` routes).
 */

import type { NextFunction, Request, Response } from "express";

export interface AuthOptions {
  baseUrl?: string;
}

export class AuthError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "AuthError";
  }
}

export async function validateGatewayKey(baseUrl: string, bearerToken: string): Promise<void> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 3000);
  try {
    const response = await fetch(`${baseUrl.replace(/\/+$/, "")}/api/teams`, {
      method: "GET",
      headers: { Authorization: bearerToken },
      signal: controller.signal,
    });
    if (!response.ok) {
      throw new AuthError(response.status, `gateway rejected key (${response.status})`);
    }
  } catch (error) {
    if (error instanceof AuthError) {
      throw error;
    }
    throw new AuthError(502, `gateway unreachable: ${(error as Error).message}`);
  } finally {
    clearTimeout(timer);
  }
}

export function createAuthMiddleware(options: AuthOptions = {}) {
  const baseUrl = (options.baseUrl ?? process.env.GATEWAY_BASE_URL ?? "http://127.0.0.1:8096").replace(/\/+$/, "");
  return async (req: Request, res: Response, next: NextFunction) => {
    if (req.path === "/health") {
      next();
      return;
    }
    const header = req.headers.authorization ?? req.headers["x-api-key"];
    if (!header) {
      res.status(401).json({ error: { type: "unauthorized", message: "Missing bearer token" } });
      return;
    }
    const bearer = String(header).toLowerCase().startsWith("bearer ") ? String(header) : `Bearer ${header}`;
    try {
      await validateGatewayKey(baseUrl, bearer);
      next();
    } catch (error) {
      const status = error instanceof AuthError ? error.status : 401;
      res.status(status).json({ error: { type: "unauthorized", message: (error as Error).message } });
    }
  };
}
