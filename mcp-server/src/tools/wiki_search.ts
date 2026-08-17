import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  query: z.string().min(1).describe("Wiki 检索词"),
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
  limit: z.number().int().min(1).max(50).default(10).describe("返回条数"),
});

export const wikiSearchTool: McpToolDef = {
  name: "wiki_search",
  description: "在团队 Wiki 知识库（02_Wiki知识库）中全文检索，返回页面标题、摘要与内部链接。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.wikiSearch(args);
    return textResult(data);
  },
};
