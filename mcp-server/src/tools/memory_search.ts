import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  query: z.string().min(1).describe("检索查询词"),
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
  identity_card_id: z.string().describe("身份卡 ID，用于 ACL 过滤").optional(),
  limit: z.number().int().min(1).max(20).default(10).describe("返回条数"),
  layer: z.enum(["l0", "l1", "l2", "l3", "l4"]).optional().describe("按层过滤"),
});

export const memorySearchTool: McpToolDef = {
  name: "memory_search",
  description: "检索团队记忆资产（L1-L4 层），返回按相关性融合排序的结果片段，支持 ACL 过滤。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.memorySearch(args);
    return textResult(data);
  },
};
