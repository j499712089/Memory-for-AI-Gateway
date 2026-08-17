import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
  content: z.string().min(1).describe("原子记忆内容（一条事实/偏好/决定/约束）"),
  source_ids: z.array(z.string()).default([]).describe("来源事件 ID（L0 event_id）"),
  confidence: z.number().min(0).max(1).default(0.8).describe("置信度 0-1"),
  visibility: z.enum(["private", "team", "restricted", "agent"]).optional().describe("可见性"),
});

export const memoryAppendTool: McpToolDef = {
  name: "memory_append",
  description:
    "写入一条 L1 候选记忆。写操作走 Gateway 的 SQLite 事务层（与 L1 提炼 Worker 同一事务路径），不直接写 Markdown 文件。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.memoryAppend(args);
    return textResult(data);
  },
};
