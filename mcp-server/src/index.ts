/**
 * MCP Server entry point.
 *
 * Usage:
 *   node dist/index.js --transport stdio   # default: stdio
 *   node dist/index.js --transport http --port 8097
 */
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { GatewayClient } from "./gateway_client.js";
import { createMcpServer, SERVER_NAME, SERVER_VERSION } from "./server.js";
import { createMcpHttpApp } from "./http.js";

interface CliArgs {
  transport: "stdio" | "http";
  port: number;
}

function parseArgs(argv: string[]): CliArgs {
  const args: CliArgs = { transport: "stdio", port: 8097 };
  for (let index = 0; index < argv.length; index++) {
    switch (argv[index]) {
      case "--transport":
        args.transport = argv[index + 1] === "http" ? "http" : "stdio";
        index++;
        break;
      case "--port": {
        const value = Number.parseInt(argv[index + 1] ?? "", 10);
        if (Number.isInteger(value) && value > 0 && value < 65536) {
          args.port = value;
        }
        index++;
        break;
      }
      default:
        break;
    }
  }
  return args;
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  if (!process.env.GATEWAY_API_KEY) {
    console.error(`[${SERVER_NAME}] WARNING: GATEWAY_API_KEY is not set; MCP tool calls will be rejected`);
  }
  const client = new GatewayClient();
  const server = createMcpServer(client);

  if (args.transport === "http") {
    const app = createMcpHttpApp({ gatewayClient: client, version: SERVER_VERSION });
    app.listen(args.port, "127.0.0.1", () => {
      console.error(`[${SERVER_NAME}] Streamable HTTP listening on http://127.0.0.1:${args.port}`);
    });
  } else {
    const transport = new StdioServerTransport();
    await server.connect(transport);
    console.error(`[${SERVER_NAME}] stdio transport ready (${SERVER_VERSION})`);
  }
}

main().catch((error) => {
  console.error(`[${SERVER_NAME}] fatal:`, error);
  process.exit(1);
});
