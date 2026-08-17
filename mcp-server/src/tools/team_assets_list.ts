import { z } from "zod";
import type { McpToolDef } from "./types.js";
import { textResult } from "./types.js";

const schema = z.object({
  team_id: z.string().describe("团队 ID（缺省取 API key 所属团队）").optional(),
  asset_type: z
    .enum(["l1", "l2", "l3", "l4", "wiki", "codegraph", "skill", "team_asset"])
    .optional()
    .describe("资产类型过滤"),
  visibility: z.enum(["private", "team", "restricted", "agent"]).optional().describe("可见性过滤"),
  status: z.string().optional().describe("状态过滤（candidate/approved/ready/...）"),
  limit: z.number().int().min(1).max(100).default(20).describe("返回条数"),
  offset: z.number().int().min(0).default(0).describe("偏移量"),
});

export const teamAssetsListTool: McpToolDef = {
  name: "team_assets_list",
  description: "列出团队资产清单（L1-L4 / Wiki / CodeGraph / Skill / Team Asset），支持类型、可见性、状态过滤。",
  inputSchema: schema,
  async call(client, rawArgs) {
    const args = schema.parse(rawArgs);
    const data = await client.teamAssetsList(args);
    return textResult(data);
  },
};
