import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  query: z.string().optional().describe("按名称/显示名/触发边界模糊过滤"),
  status: z.enum(["candidate", "approved", "deprecated"]).optional().describe("技能状态"),
  version: z.string().optional().describe("精确 SemVer 版本"),
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
});

export const skillSearchTool: McpToolDef = {
  name: "skill_search",
  description: "检索团队技能库（04_技能库），返回技能版本、状态、触发边界与步骤摘要。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.skillSearch(args);
    return textResult(data);
  },
};
