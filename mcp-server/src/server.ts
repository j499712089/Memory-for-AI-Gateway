import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { GatewayClient } from "./gateway_client.js";
import { tools } from "./tools/index.js";

export const SERVER_NAME = "memory-gateway";
export const SERVER_VERSION = "0.1.0";

export function createMcpServer(client: GatewayClient): McpServer {
  const server = new McpServer({ name: SERVER_NAME, version: SERVER_VERSION });
  for (const tool of tools) {
    server.registerTool(
      tool.name,
      { description: tool.description, inputSchema: tool.inputSchema },
      async (args) => {
        try {
          return await tool.call(client, args);
        } catch (error) {
          return {
            content: [{ type: "text", text: JSON.stringify({ error: (error as Error).message }) }],
            isError: true,
          };
        }
      },
    );
  }
  return server;
}
