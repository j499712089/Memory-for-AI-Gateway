import type { z } from "zod";
import type { GatewayClient } from "../gateway_client.js";

export interface ToolTextContent {
  type: "text";
  text: string;
}

export interface ToolResult {
  content: ToolTextContent[];
  isError?: boolean;
  [key: string]: unknown;
}

export interface McpToolDef<Schema extends z.ZodTypeAny = z.ZodTypeAny> {
  name: string;
  description: string;
  inputSchema: Schema;
  call: (client: GatewayClient, rawArgs: unknown) => Promise<ToolResult>;
}

/** Serializes structured tool output as a text block (data-access tools). */
export function textResult(data: unknown): ToolResult {
  return { content: [{ type: "text", text: JSON.stringify(data, null, 2) }] };
}

export function errorResult(message: string): ToolResult {
  return { content: [{ type: "text", text: JSON.stringify({ error: message }) }], isError: true };
}
