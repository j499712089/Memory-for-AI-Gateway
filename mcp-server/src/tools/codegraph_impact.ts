import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  symbol: z.string().optional().describe("要分析的符号名（与 file_path 二选一）"),
  file_path: z.string().optional().describe("限定符号所在文件路径（与 symbol 二选一）"),
  repo_id: z.string().min(1).describe("代码仓库 ID（code_repos.id）"),
  depth: z.number().int().min(1).max(5).default(3).describe("调用链深度"),
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
});

export const codeGraphImpactTool: McpToolDef = {
  name: "codegraph_impact",
  description: "分析 CodeGraph 中指定符号的调用链（callers/callees）与受影响文件，depth 控制遍历深度。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.codeGraphImpact(args);
    return textResult(data);
  },
};
