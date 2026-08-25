import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z
  .object({
    session_id: z.string().optional().describe("会话 ID"),
    conversation_id: z.string().optional().describe("上游对话标识（与 session_id 二选一）"),
  })
  .refine((value) => value.session_id !== undefined || value.conversation_id !== undefined, {
    message: "session_id 与 conversation_id 必须至少提供一个",
  });

export const sessionBindingGetTool: McpToolDef = {
  name: "session_binding_get",
  description: "按 session_id 或 conversation_id 查询当前会话绑定（team/agent/身份卡/上游通道）。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.bindingGet(args);
    return textResult(data);
  },
};
