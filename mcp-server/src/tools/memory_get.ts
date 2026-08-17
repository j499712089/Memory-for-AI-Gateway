import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  asset_id: z.string().min(1).describe("资产 ID（memory_search / team_assets_list 返回）"),
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
});

export const memoryGetTool: McpToolDef = {
  name: "memory_get",
  description: "按资产 ID 读取一条记忆资产的完整正文与元数据（经 ACL 过滤）。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.memoryGet(args);
    return textResult(data);
  },
};
