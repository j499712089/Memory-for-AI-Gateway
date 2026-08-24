# Obsidian 文件结构与格式规范（ALL-69）

> 版本: 2.1 · 日期: 2026-08-24 · 作者: 首席架构师-高见远
> v2.1 修订（ALL-201 R2-3）：§5.4 `ops_outbox.op_type` 补 `NOT NULL` + CHECK 约束
> （原为行尾注释，不生效）。其余章节与 v2.0 相同。
> 上游依据: ALL-66 Memory Asset 统一模型（ALL-66 §5 必填/可选汇总：`tags` 为**可选**）、
> ALL-67 核心 Schema、ALL-68 扩展 Schema、ADR-004（同步协议，本文档配套）
> 本文档可直接作为 Obsidian 插件或同步脚本的实现依据。

## 0. 设计原则

1. **权威矩阵（本版唯一权威表述，取代 v1.0 的"Obsidian 是 Source of Truth"单一表述）**：
   文件系统与 SQLite 的权威性**按字段分派**，不存在"一方整体权威"——这消除了
   v1.0 §0.1（Obsidian 权威）与 §2.5/§7.1（SQLite 覆盖文件、库为权威）之间的双重解释。
   - **A 类 · 文件权威（用户可写，文件 → 库）**：`title`、正文 `content`、`tags`、
     `## Relations` 关系段、`category`、`language`、`summary`、`status`、
     `reviewed_by`/`reviewed_at`、`expires_at`。
   - **B 类 · 库权威（系统派生，库 → 仅回写文件对应键）**：`memory_id`、`version`、
     `slug`、`source_path`、`created_at`/`updated_at`、`archived_at`。
   - **C 类 · 库私有（只在 SQLite，绝不落文件）**：`content_hash`、`chunk_count`、
     `embedding_model`、embedding 向量、`last_accessed_at`、`memory_chunks`/`memory_fts`/
     `memory_scopes`/`memory_versions`/`memory_events` 全部行。
   - **冲突仲裁总规则**：A 类字段两侧不一致 → **文件胜**（用户编辑不被覆盖，回写库）；
     B 类字段两侧不一致 → **库胜**（文件侧由库回写）；C 类不存在于文件，无冲突。
2. **SQLite 必须可由文件全量重建**：A 类字段在 Frontmatter/正文中是完整可读的；
   ALL-67 主表 + ALL-68 扩展表的 A 类字段全部能由 `walk(vault)` 恢复（见 §7.4 重建策略）。
3. **两层写入协议**：文件写 = 临时文件 + 原子 rename（文件系统无事务，单文件操作原子）；
   「文件操作 + 库操作」的跨域一致性由 **ops_outbox 补偿协议**保证（§5.4、§7.3），
   本规范**不再声称**"文件与 SQLite 在同一事务内提交"（物理上不可能）。
4. **P0 视觉规则**：全项目不把 emoji 用作功能/分类图标；功能图标统一使用
   **Lucide**（Obsidian 内置图标集，SVG，无额外依赖），见 §4.3。

## 1. 目录分层规则

### 1.1 顶层目录（7 类类型目录 + 3 个特殊目录，统一下划线前缀）

```
vault/
├── identity/      # memory_type=identity 身份记忆
├── code/          # memory_type=code 代码记忆
├── wiki/          # memory_type=wiki 稳定知识
├── chat/          # memory_type=chat 对话记忆
├── team/          # memory_type=team 团队记忆
├── experience/    # memory_type=experience 经验教训
├── decision/      # memory_type=decision 决策记录
├── _templates/    # 特殊目录：新建笔记模板（不上行到 memories 表；扫描器跳过）
├── _archive/      # 特殊目录：status=archived 的记忆（**参与扫描**，见 §1.4）
└── _trash/        # 特殊目录：软删除暂存（30 天后物理清理；扫描器跳过）
```

- **特殊目录命名统一为 `_templates` / `_archive` / `_trash`（下划线前缀）**：
  v1.0 §1.1 目录树写 `templates/archive/trash` 而正文写"加下划线前缀"，自相矛盾。
  v2.0 统一为下划线形式——避免与 7 个类型目录混用（类型目录不可能以 `_` 开头），
  且下划线在 Windows 排序中固定靠前。目录树、扫描器（§5.2）、归档路径示例（§7.2）
  全部使用同一形式。
- **禁止**在 vault 根目录放业务笔记文件：根目录只允许以上 10 个目录与 `README.md`；
  扫描器遇到未知顶层项时跳过并写告警日志，不报错中断。
- `_archive/`、`_trash/` 是**逻辑分区的物理映射**，不是第三种类型：文件移入后
  Frontmatter 的 `type` 不变，只变更 `status` 与 `source_path`。
- `_templates/`：模板文件不参与同步（不上行、不建 memories 行），扫描器整树跳过。

### 1.2 子目录组织规则

子目录按 **scope 段 + 时间段** 两级组织，路径模板：

```
<memory_type>/<scope_segment>/<YYYY>/<MM>/<filename>.md
```

| 段 | 规则 | 示例 |
|---|---|---|
| scope_segment | `global` / `role-<uuid>` / `team-<uuid>` / `project-<uuid>` / `private`，**id 取完整 UUID（36 字符，含连字符）** | `team-3f6a2c81-1111-2222-3333-444455556666/` |
| YYYY / MM | `created_at` 的 UTC 年/月，两位月 | `2026/08/` |

- **scope 段必须用完整 UUID，不用前 8 位**（v1.0 的 `team-<id8>` 是"未定义映射"：
  前 8 位不是可逆编码、无冲突检测，8 位十六进制在万级/十万级 id 下存在碰撞可能，
  而段名碰撞会直接造成路径错配）。完整 UUID 是**全量、可逆**的授权映射，
  `role_id` ↔ 目录段 = 一一对应，无需碰撞补偿。
- **碰撞测试**（随同步器实现，一次性）：导入历史数据后遍历
  `SELECT role_id, COUNT(*) FROM memories WHERE scope_type='role' GROUP BY role_id HAVING COUNT(*)>0`
  并反转映射 `path_segment → id`，断言所有 id 映射唯一且与 `memories.role_id/team_id/project_id`
  逐行一致；测试样例见 `ALL-196` 汇总（含 0/8 位前缀的场景回归）。
- 示例完整路径：

```
code/team-3f6a2c81-1111-2222-3333-444455556666/2026/08/20260824T093512Z-auth-token-refresh.md
wiki/global/2026/08/20260824T093512Z-https-tls-handshake.md
```

- scope 段 + 时间段的组合**生成后只读**：修改 scope 或时间不产生新路径的语义变更，
  文件按 §7.3 迁移到新路径。
- 一个 scope 只映射到一个 scope_segment（`scope_type` 决定段前缀，段内值取对应 id 全量）；
  多 scope 授权不镜像目录，只反映在 `memory_scopes` 表与 `scope_type` 主字段上。
- 段内目录只在有文件落地时创建；空目录由同步器幂等清理。

### 1.3 scope_segment 与 scope_type 的映射

| scope_type | scope_segment | 段内值 | 与 ALL-67 字段一致 |
|---|---|---|---|
| global | `global` | 无 | 无联动字段 |
| role | `role-<uuid>` | role_id 全 UUID | role_id |
| team | `team-<uuid>` | team_id 全 UUID | team_id |
| project | `project-<uuid>` | project_id 全 UUID | project_id |
| private | `private` | 无 | visibility=private（强制） |

### 1.4 `_archive/` 参与扫描（可重建性证明）

- `_archive/` **不排除在扫描之外**：扫描器对 `_archive/` 目录执行**只读元数据解析**——
  读 Frontmatter（`status: archived`、`archived_at`、`memory_id`、完整 A 类字段）
  与正文，用于：(a) 与库中 archived 行双向校准（文件为准回写 A 类字段）；
  (b) **重建存档**（§7.4）。归档文件**不重新分块、不重新 embedding**（没有检索需求），
  只保留 `memory_chunks`/`memory_fts` 的既有数据或清空标记。
- **archived 文件可重建证明**：`_archive/` 下的文件保留**完整 Frontmatter + 完整正文**
  （只是 `status: archived` 与 `archived_at` 被回写），因此任何时点都可从
  `_archive/<memory_type>/...` 文件完整恢复该记忆的 A 类字段并重建库行。
- `_trash/` 与 `_templates/` 不参与同步（`_trash` 的物理生命周期见 §7.2，`_templates` 纯模板）。

## 2. Frontmatter 字段规范（与 SQLite 双向映射）

Frontmatter 使用 YAML，必须位于文件首行与 `---` 之间。键名与 ALL-66 §7 映射表一致：
`memory_type` 在 Frontmatter 中缩写作 `type`（其余键同名同值）。

### 2.1 必填字段

| Frontmatter key | SQLite 列 | 类型 | 约束 | 权威类 |
|---|---|---|---|---|
| `type` | memory_type | string | 7 枚举值之一 | B |
| `memory_id` | memory_id | string | UUID v4 | B |
| `title` | title | string | 1–200 字符 | A |
| `scope_type` | scope_type | string | 5 枚举值之一 | A |
| `visibility` | visibility | string | public/shared/private，默认 shared | A |
| `role_id` / `team_id` / `project_id` | 同名列 | string? | 按 scope_type 条件必填（§2.3） | A |
| `owner_id` | owner_id | string | 归属者 | A |
| `source_type` | source_type | string | agent/human/import/derived/system | A |
| `created_by` | created_by | string | 创建者 id | A |
| `status` | status | string | 6 枚举值，默认 candidate | A |
| `version` | version | integer | ≥1，系统派生，禁止手改 | B |
| `importance` | importance | integer | 0–10，默认 5 | A |
| `confidence` | confidence | number | 0.0–1.0，默认 1.0 | A |
| `created_at` | created_at | datetime | ISO 8601，UTC，`YYYY-MM-DDTHH:MM:SSZ` | B |
| `updated_at` | updated_at | datetime | ISO 8601，UTC | B |

> **v2.0 修正**：`tags` 从必填移入可选（ALL-66 §5 明确 `tags` 属于"可选"组）——
> 不得一边声明"与 ALL-66 精确对齐"一边设为必填。空 tags = `tags: []` 或省略键，
> 同步器二者等价处理（§2.2）。

### 2.2 可选字段

| Frontmatter key | SQLite 列 | 类型 | 约束 | 权威类 |
|---|---|---|---|---|
| `tags` | memory_tags | string[] | 数组，去重，每项 1–50 字符；**可选**，空数组或省略 | A |
| `category` | category | string | ≤ 50 字符 | A |
| `language` | language | string | zh/en/multilingual | A |
| `source_id` | source_id | string | 来源对象 id | A |
| `reviewed_by` / `reviewed_at` | 同名列 | string / datetime | 同生同灭（§2.3） | A |
| `derived_from` | derived_from | string | memory_id；与关系段双向一致 | A |
| `summary` | summary | string | ≤ 200 字符 | A |
| `archived_at` | archived_at | datetime | status=archived 时必有 | B |
| `expires_at` | expires_at | datetime | 可选 TTL | A |

> 系统派生与库私有字段（`content_hash`/`chunk_count`/`embedding_model`/embedding/`slug`/
> `source_path`/`last_accessed_at`）不写 Frontmatter（§2.4）。

### 2.3 条件必填与联动（与 ALL-67 CHECK 约束一致）

| 条件 | 规则 |
|---|---|
| scope_type=role | 必填 role_id |
| scope_type=team | 必填 team_id |
| scope_type=project | 必填 project_id |
| scope_type=private | 强制 visibility=private |
| source_type=derived | 必填 derived_from，且关系段中必有对应 derived_from 关系 |
| status=review 且进入 active | reviewed_by 与 reviewed_at 必须同时出现 |
| status=archived | 必有 archived_at；非 archived 时 archived_at 必须为空 |

### 2.4 系统派生字段（禁止手写）

同步器从正文与事件推导，Frontmatter 中出现即视为不一致并纠正：

| Frontmatter 中不出现 | 原因 |
|---|---|
| `content_hash` | 由正文 SHA256 派生（C 类） |
| `chunk_count` | 由 memory_chunks 计数派生（C 类） |
| `embedding_model` / embedding | 只存 Registry 与向量层（C 类） |
| `slug` | 由文件名派生（B 类） |
| `source_path` | 就是文件自身路径（B 类） |
| `last_accessed_at` | 访问统计，不属文件内容（C 类） |

### 2.5 与 SQLite 的同步方向矩阵（按权威类执行）

| 方向 | 触发 | 处理 |
|---|---|---|
| 文件 → SQLite | 文件变更监听（fsnotify）或全量 diff 扫描 | 解析 Frontmatter/正文，**A 类字段以文件为准** upsert 库行；重算 content_hash，变则重分块 + 重 embedding |
| SQLite → 文件 | 服务写路径（创建/版本升级在 D 类字段变化时） | **只回写 B 类字段**（memory_id/version/updated_at/archived_at）；A 类字段库侧已存在时**不得覆盖文件** |
| 冲突 | 两侧**同一字段**都变更（按类区分） | A 类 → 文件胜（§7.1）；B 类 → 库胜（例：version 冲突以库为准，文件回写）；C 类无冲突 |

### 2.6 Frontmatter 示例（完整性检查用）

```yaml
---
type: code
memory_id: 7f3b2d1c-9e4a-4b56-8c2d-0a1b2c3d4e5f
title: Auth Token 刷新实现
tags:
  - auth
  - token
  - refresh
scope_type: team
team_id: 3f6a2c81-1111-2222-3333-444455556666
visibility: shared
owner_id: 4acfe9c1-18cb-4370-b898-ca0186f274e0
source_type: agent
created_by: 3ac7e6de-db63-42ac-88d2-e935fbd4eaa5
status: active
version: 2
importance: 8
confidence: 0.9
language: zh
category: auth
created_at: 2026-08-20T08:15:00Z
updated_at: 2026-08-24T06:40:12Z
---
```

## 3. Markdown 正文格式

1. **标题层级**：正文从 `##`（H2）开始，`#`（H1）禁止出现——H1 语义由 Frontmatter
   `title` 承担。H2 为段落起点，H3–H6 逐级嵌套。
2. **正文结构**：首段为 1–2 句摘要；随后按记忆类型使用推荐模板（§3.4）。
3. **内链（wikilink）**：
   - 链接语法 `[[目标文件名]]`；链接的是**文件名（不含扩展名）**，解析时先按
     `memory_type/slug` 唯一键解析，失败再按全库 slug 唯一查找，仍失败则记录
     `broken_link` 告警。
   - 关系标注使用**独立 Relations 段**而非内嵌链接，供 SQLite 关系表同步：
     ```markdown
     ## Relations
     related_to:: [[20260824T093512Z-https-tls-handshake]]
     derived_from:: [[20260820T081500Z-tls-cert-rotation]] (weight: 0.8)
     ```
   - 语法为 `key:: [[...]] `，`key` 取值即 6 种 relation_type：
     `caused_by` / `related_to` / `derived_from` / `contradicts` / `updates` /
     `superseded_by`；可选 `(weight: 0.0–1.0)`，默认 1.0。
   - 同一 key 多行展开为多条关系；`derived_from` 出现时同步写入 Frontmatter 的
     `derived_from` 字段（双向一致，A 类）。
4. **标签（tag）**（A 类，文件权威）：
   - Frontmatter 的 `tags` 是权威来源（**可选**，空数组或省略 = 无标签），同步进
     `memory_tags`（双向映射）；
   - 正文中内联 `#tag` 仅为便捷输入，同步时合并去重（大小写不敏感），拆分规则：
     全角/半角空格、逗号、分号、`[`、`]`、`(`、`)`、`"`、`'`；
   - 标签允许 CJK（如 `#数据库`）、允许 `/` 表示层级（如 `#auth/token`），
     禁止空白与上述拆分符。
5. **代码块**：必须带语言标识的围栏：

   ````markdown
   ```go
   func main() {}
   ```
   ````

   无语言标识裸代码块视为规范违规（告警级）；代码片段在 `code/` 类型下要求给出：
   目的、输入输出、坑（可选）。**代码块内部不使用 Markdown 转义，不做逐行修订**，
   变更检测以 content_hash 为准。
6. **元数据块**：机器可读的附加 JSON 可用 ```` ```json ```` 围栏放在文末
   `## Metadata` 段下，仅用于同步器私有字段（如导入源时间戳），
   不得存放与 §2 键冲突的字段。

### 3.4 各类型正文模板（骨架，非强制）

| 类型 | 推荐段落 |
|---|---|
| identity | ## 角色定位 / ## 能力边界 / ## 偏好与禁忌 |
| code | ## 用途 / ## 代码 / ## 调用示例 / ## 坑与注意 |
| wiki | ## 定义 / ## 要点 / ## 相关 |
| chat | ## 上下文 / ## 讨论要点 / ## 结论 |
| team | ## 成员与职责 / ## 协作约定 / ## 变更记录 |
| experience | ## 背景 / ## 经过 / ## 原因分析 / ## 改进措施 |
| decision | ## Context / ## Options / ## Decision / ## Consequences / ## Alternatives Considered |

## 4. 文件命名规则

### 4.1 命名模式

```
<UTC时间戳>T<UTC时间>Z-<slug>.md
  时间戳:  YYYYMMDD + "T" + HHMMSS + "Z"   （如 20260824T093512Z）
  slug:    见 §4.2，全小写 ASCII + CJK，不含空格
```

- 时间戳取 `created_at`，UTC，不用本地区时区；
- 两个文件的时间戳相同（同秒创建）不视为冲突，slug 兜底（§4.4）。

### 4.2 slug 生成算法

```
1. 标题 Unicode 规范化（NFKC）
2. 移除前后空白；内部连续空白/中划线/下划线 归并为单个 '-'
3. 保留 [a-z0-9] 与 CJK（\p{Han}）字符；其余字符按表 4.3 转义或删除
4. 小写化（仅 ASCII 部分）
5. 保留的 '-' 不得超过连续 1 个；首尾 '-' 删除
6. 若为空 → 使用 memory_id 前 12 位
7. 若超过 60 字符 → 截断至 57 + '-' + md5(memory_id)[0:2]
8. 命中 Windows 保留名 → 前缀 'n-'（§4.3）
```

### 4.3 非法字符与转义规则

Windows 文件系统 + Obsidian 的禁用集合合并如下（命中即按表处理）：

| 字符/情形 | 处理 |
|---|---|
| `\ / : * ? " < > |` | 替换为 `-`（连续替换合并为一个 `-`） |
| 控制字符 U+0000–U+001F | 删除 |
| 首尾空格、尾随 `.` | 删除 |
| 保留名（CON/PRN/AUX/NUL/COM1-9/LPT1-9，含带扩展名形式） | 前缀 `n-`（如 `n-con.md`） |
| emoji（功能图标用途，P0 规则） | 文件名中一律不使用；正文中不作为分类/图标 |
| 全角冒号 `：` | 允许保留（非 Windows 禁用字符） |

### 4.4 冲突处理策略

| 冲突场景 | 策略 |
|---|---|
| 同一 `(memory_type, slug)` 与库中已有记录 | 已存在时 slug 追加 `-2`、`-3`…（幂等重试），不改时间戳 |
| 目标 `source_path` 已被占用 | 该文件 Frontmatter 中 `memory_id` 与实际库记录不符时，按跨记忆冲突处理：新文件 slug 追加数字后缀 |
| 同一 memory（同 memory_id）双向内容变更 | 见 §7.1 冲突副本机制 |
| 目录已存在同名（大小写不同） | Windows 视为同路径：先比较内容 hash，相同则合并，不同则按 §7.1 |
| 文件名截断后与既有文件碰撞 | slug 追加 `-<md5(memory_id)[0:2]>` 兜底 |

## 5. 平台与工具约束

### 5.1 Obsidian 侧

- 仅使用 **markdown 源文件**，不启用 Obsidian 加密（vault 内容由外层存储加密）。
- `[[wikilink]]` 关闭"自动更新链接"以外的破坏性行为；Obsidian 的 `.obsidian/`
  工作区配置目录由扫描器排除，不参与同步。
- 建议在 `.obsidian/app.json` 打开 `newFileLocation: folder` 与
  `attachmentFolderPath: attachments`，保证新文件也有受控路径。

### 5.2 同步器侧（扫描范围）

- 文件扫描顺序：按 memory_type、scope 段、时间段的字典序，保证幂等。
- **扫描范围与排除**：

  | 路径 | 处理 |
  |---|---|
  | 7 个类型目录 | 正常同步（upsert） |
  | `_archive/` | **解析元数据**（只读）：校准/重建 archived 行（§1.4），不重算 chunk/embedding |
  | `_trash/` | 跳过（物理生命周期 §7.2） |
  | `_templates/` | 跳过（不上行） |
  | `.obsidian/`、`.git/`、`.multica/` | 跳过 |

- 所有 Frontmatter 解析失败（YAML 语法错误）→ 该文件记 `parse_error` 事件并跳过，
  不动原文件；解析成功但字段校验失败 → 写 `validation_warning`，按其值取值或取默认。
- 文件写入使用临时文件 + rename 原子替换（§7.3 协议）；每次文件变更都写 `memory_events`
  （event_type: `updated` / `created` / `archived`）。

### 5.3 Windows 路径预算

完整最长路径（260 字符 MAX_PATH 之内）：

```
<memory_type>/<scope_segment>/<YYYY>/<MM>/<timestamp>-<slug>.md
```

- `code/` = 5，`team-<uuid>/` = 42（"team-" 6 + UUID 36 + "/" 1），`2026/` = 5，`08/` = 3，
  时间戳 16，`-` 1，slug ≤ 60，`.md` = 3 → 键槽合计 ≈ 135，
  加仓库根路径之后若仍超限，启用长路径支持或缩短 slug 至 40（全 UUID 段不可缩短，见 §1.2）。

### 5.4 跨域一致性：ops_outbox 补偿协议（替代 v1.0 的"同一事务"表述）

文件系统与 SQLite **不能**在单个 ACID 事务内提交（跨域无共同事务管理器）。
v1.0 §0.3"文件路径 + Frontmatter + 正文三者由写入服务在同一事务内生成或更新"
的表述作废。替代协议（写入服务 in-process，SQLite 表 `ops_outbox`）：

```
ops_outbox(
  op_id TEXT PRIMARY KEY,          -- UUID
  op_type TEXT NOT NULL CHECK (op_type IN ('create','update','move','archive','trash','purge')),
  memory_id TEXT NOT NULL,
  src_path TEXT,                   -- 变更前路径（move/update 用）
  dst_path TEXT NOT NULL,          -- 目标路径
  content_hash TEXT,               -- 本次写入正文的 SHA256
  phase TEXT NOT NULL DEFAULT 'pending' CHECK (phase IN ('pending','committed','done','failed')),
  created_at TEXT NOT NULL DEFAULT (datetime('now','utc')),
  last_attempt_at TEXT,
  attempt_count INTEGER NOT NULL DEFAULT 0
)
```

> **v2.1 修订（ALL-201 R2-3）**：`op_type` 由「仅行尾注释列出六个取值」改为 **DB 层
> CHECK 约束**，与 `phase` 对齐。此前注释不产生任何约束，非法 op_type（如
> `'TOTALLY_BOGUS'`）会被 SQLite 静默接受，而恢复协议按 `op_type` 分派驱动器——
> 未知取值将命中 `DRIVERS` 查表失败，把一次写入错误推迟成恢复期崩溃。约束前置后，
> 非法值在插入点即被拒绝。`op_type` 同时补 `NOT NULL`：outbox 行没有操作类型不可恢复。

**执行顺序（每类操作，崩溃任意点可恢复）**：

| 操作 | 顺序 |
|---|---|
| create/update | ① 写 `ops_outbox(pending)` + 库行变更（**同一 SQLite 事务**）；② 写临时文件 + 原子 rename 到 `dst_path`；③ 回写 B 类字段到 Frontmatter；④ outbox → `done` |
| move（scope 迁移/归档/trash） | ① outbox(pending)；② **先写目标路径**（临时 + rename，内容或完整 Frontmatter 版本）；③ 库行 `source_path` → `dst_path`，`status`/`archived_at` 同步（同一 SQLite 事务）；④ 删除旧路径文件；⑤ outbox → `done` |
| purge（trash 物理删除） | ① outbox(pending)；② 删除文件；③ 库行软删除确认置 `purged`（保留 event 历史）；④ `done` |

**恢复协议（启动时 / 每次 sync_once 前，按 phase 重放）**：

| 崩溃点 | 恢复动作 |
|---|---|
| ① 后（outbox pending，文件未动） | 幂等重做 ②：目标不存在 → 写入；目标存在且 hash 一致 → 跳过（跳到 ③）；hash 不一致 → 记 `conflict` 事件，按 §7.1 |
| ② 后（文件已落，库/outbox 未推进） | 校验目标文件 hash == outbox.content_hash → 完成 ③④⑤；不等 → 冲突处理 |
| ③ 后（库已更新，旧文件未删） | 幂等：旧路径存在且属于本 memory_id → 删除；不存在 → 已进步，跳过 |
| ④ 后 | outbox → `done`（幂等） |
| 连续 3 次失败的 op | phase → `failed`，写 `memory_events(event_type='sync_failure', payload={op_id, reason})`，不阻塞后续 op，人工/Agent 复核 |

- **move 的关键约束**：**绝不允许"先更新 `source_path` 再移动文件"**（v1.0 §7.3 的表述，
  崩溃后库指向不存在的路径）。新顺序是"目标路径先生效，库再跟进，旧路径最后清"，
  任意时点至多存在一个"库指向缺失文件"的窗口，且该窗口由恢复协议闭合。
- 恢复协议幂等性验证：`test_sync_outbox.py`（ALL-196 附录）注入 DB commit 前/后、
  文件 rename 前/后、move 前/后、purge 前/后崩溃，断言重放后状态一致（§7.4 验收）。

## 6. 完整文件示例

### 示例一：code 类型（team 范围）

**路径**：`code/team-3f6a2c81-1111-2222-3333-444455556666/2026/08/20260824T093512Z-auth-token-refresh.md`

````markdown
---
type: code
memory_id: 7f3b2d1c-9e4a-4b56-8c2d-0a1b2c3d4e5f
title: Auth Token 刷新实现
tags:
  - auth
  - token
  - refresh
scope_type: team
team_id: 3f6a2c81-1111-2222-3333-444455556666
visibility: shared
owner_id: 4acfe9c1-18cb-4370-b898-ca0186f274e0
source_type: agent
created_by: 3ac7e6de-db63-42ac-88d2-e935fbd4eaa5
status: active
version: 2
importance: 8
confidence: 0.9
language: zh
category: auth
created_at: 2026-08-20T08:15:00Z
updated_at: 2026-08-24T06:40:12Z
---

## 用途

在访问令牌过期前 60 秒自动刷新，避免请求 401。

## 代码

```go
func refreshToken(c *http.Client, store *TokenStore) error {
    tok, err := store.Load()
    if err != nil {
        return err
    }
    if time.Until(tok.ExpiresAt) > 60*time.Second {
        return nil
    }
    fresh, err := c.Exchange(tok.RefreshToken)
    if err != nil {
        return fmt.Errorf("refresh: %w", err)
    }
    return store.Save(fresh)
}
```

## 坑与注意

- refresh token 轮换后旧值立即失效，重试不得复用旧 token。
- 并发刷新必须加 per-user 互斥锁，否则第二次交换返回 invalid_grant。

## Relations

updates:: [[20260820T081500Z-tls-cert-rotation]]
related_to:: [[20260824T091200Z-api-rate-limit]] (weight: 0.6)
```
````

### 示例二：decision 类型（global 范围）

**路径**：`decision/global/2026/08/20260824T101500Z-vector-storage-adapter.md`

````markdown
---
type: decision
memory_id: a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d
title: Vector 存储通过可替换 Adapter 接入
scope_type: global
visibility: public
owner_id: 4acfe9c1-18cb-4370-b898-ca0186f274e0
source_type: agent
created_by: 4acfe9c1-18cb-4370-b898-ca0186f274e0
status: active
version: 1
importance: 9
confidence: 1.0
language: zh
created_at: 2026-08-24T10:15:00Z
updated_at: 2026-08-24T10:15:00Z
---

## Context

记忆库需要离线语义检索，且启动项必须可替换（sqlite-vec 为 pre-1.0）。

## Options

- sqlite-vec（vec0 虚拟表，UDF 免 API）
- Faiss 独立索引文件 + ID 同步
- Qdrant / Milvus 服务化

## Decision

采用 sqlite-vec，但只通过 `VectorIndex` 接口访问，替换扩展仅改装配：
`VectorIndex.Upsert/Delete/Search(vector, modelKey, limit)`。

## Consequences

- 正面：单数据库边界、备份恢复简单、无独立向量服务进程。
- 负面：上游 pre-1.0，升级可能破坏 API；一百万 chunk 前需基准实测。
- 风险：模型升级需全量重算向量，由模型键切换触发。

## Alternatives Considered

Faiss 把索引生命周期与 ID 一致性变成应用责任而被否决；服务化方案在
10 万 chunk 以下运维复杂度不成比例，延期。

## Relations

related_to:: [[20260824T093512Z-auth-token-refresh]] (weight: 0.3)
```
````

> 示例二展示 `tags` 可选：无 tags 时省略键（合法，同步为 `memory_tags` 空集）。

## 7. 双向同步与冲突、重命名策略

### 7.1 双向并发版本冲突（按权威类仲裁）

规则：**SQLite 侧与 Obsidian 侧各自持有 content_hash，比较两侧 hash**；
A 类字段以文件为准，B 类以库为准。

| 情形 | 处理 |
|---|---|
| 两侧 hash 相同 | 幂等，不动作 |
| 仅库侧变（B 类字段，如 version 升级） | 回写 Frontmatter 的 B 类键（不触碰正文）——库胜 |
| 仅文件变 | 以文件为准回写库（含重新分块、重算 embedding 标记）——文件胜 |
| 两侧都变且不同 | **不自动合并**：文件原样保留为新文件 `<原名>.conflict-<hash8>.md`，写 `memory_events`（event_type: `updated`，payload: `{"conflict": true}`），并通知 owner 待人工/Agent 复核；库行冻结在变更前版本（`version+1` 待合并后落地） |

### 7.2 状态迁移的物理移动（统一 `_archive/`/`_trash/` 命名与 outbox 协议）

- `candidate/review/active/updated/deprecated` 留在类型目录；
- `→ archived`：经 ops_outbox move 协议移动到 **`_archive/<memory_type>/...`**，
  Frontmatter 回写 `status: archived` 与 `archived_at`（B 类，库胜）；
  `_archive/` 参与元数据扫描（§1.4），文件完整保留 → 可重建；
- `→ trash`（软删除前）：移动到 **`_trash/`**；30 天后由清理任务物理删除并级联清理 Registry；
  物理删除前必须无其他记忆 `derived_from` 引用它（ALL-67 触发器约束）；
  `_trash/` 目录由扫描器跳过（§5.2），`_trash` 内文件的恢复 = 从 `_trash/` 拖回类型目录
  （sync 会按库行 `status` 校准）。

### 7.3 重命名与迁移策略

- **slug 一经生成即冻结**（重命名 title 不改动文件名，只改 Frontmatter `title`），
  保证 `[[wikilink]]` 永不失效；
- scope 变更（如 project → team）：经 ops_outbox move 协议移动到新 `<scope_segment>/` 目录，
  顺序为"目标路径先生效 → 库 `source_path` 跟进 → 旧路径清删"（§5.4），
  **不改变 slug 与 memory_id**；
- 已发布 wikilink 不因 scope 迁移失效：wikilink 解析按"先 memory_type/slug，再全库 slug，
  最后按 memory_id"三级兜底；
- 显式重命名只用于标题存在 typo 且从未被引用时，由人工在 Obsidian 中 rename，
  同步器按新 slug 重建索引。

### 7.4 重建策略（SQLite 随时可由文件重来）

```
rebuild(force: bool):
  1. walk(vault) 采集全部 md（含 _archive/ 元数据；跳过 _templates/_trash/.obsidian/.git）
  2. 解析 Frontmatter → 以 memory_id 为主键重建 memories 的 **A 类字段**（文件为准）
     + B 类派生（source_path/slug/version 取 max(库, 文件)/created_at 以文件创建时间为准）
  3. 重算 content_hash；与库内既有 hash 相同 → 跳过 chunk/embedding 重算（增量优化）
  4. hash 不同或文件新增 → 重建 memory_chunks/memory_fts/memory_tags/relations
  5. 库中 memory_id 在文件中不存在 → 保持原行（不自动删除），标 `orphaned` 待人工
  6. C 类（versions/events/scopes）以库为准合并；embedding 按 §4(ALL-70) 模型键重算
```

- 重建**幂等**可重复执行；默认**增量**（hash 未变不动），`force=true` 为全量重算。
- "库有未提交修改"的定义（ADR-004 同步）：存在 `ops_outbox` 中 phase ∈
  {pending, committed, failed} 的行，或 `sync_state.last_indexed_at < memories.updated_at`。
  两类状态下禁止执行会覆盖文件 A 类字段的回写。

## 8. 字段映射总表（Frontmatter ↔ SQLite）

| ALL-66 字段 | SQLite 列 | Frontmatter key | 方向 | 必填 | 权威 |
|---|---|---|---|---|---|
| memory_id | memories.memory_id | `memory_id` | 双向 | 是 | B |
| memory_type | memories.memory_type | `type` | 双向 | 是 | B |
| title | memories.title | `title` | 双向 | 是 | A |
| slug | memories.slug | （文件名派生） | SQLite→文件 | 是 | B |
| category | memories.category | `category` | 双向 | 否 | A |
| tags | memory_tags.tag | `tags` | 双向 | **否** | A |
| importance | memories.importance | `importance` | 双向 | 是 | A |
| confidence | memories.confidence | `confidence` | 双向 | 是 | A |
| language | memories.language | `language` | 双向 | 否 | A |
| source_type | memories.source_type | `source_type` | 双向 | 是 | A |
| source_id | memories.source_id | `source_id` | 双向 | 否 | A |
| created_by | memories.created_by | `created_by` | 双向 | 是 | A |
| reviewed_by | memories.reviewed_by | `reviewed_by` | 双向 | 条件 | A |
| derived_from | memories.derived_from | `derived_from` + `## Relations` | 双向 | 条件 | A |
| source_path | memories.source_path | （文件自身路径） | 文件→SQLite | 是 | B |
| content | memories.content | （Markdown 正文） | 双向 | 是 | A |
| content_hash | memories.content_hash | （派生，不写） | SQLite→派生 | 是 | C |
| summary | memories.summary | `summary` | 双向 | 否 | A |
| embedding_model | memories.embedding_model | （不写） | 无 | 否 | C |
| chunk_count | memories.chunk_count | （不写） | 无 | 否 | C |
| scope_type | memories.scope_type | `scope_type` | 双向 | 是 | A |
| visibility | memories.visibility | `visibility` | 双向 | 是 | A |
| scope ids | memories.role_id/team_id/project_id | 同名列 | 双向 | 条件 | A |
| owner_id | memories.owner_id | `owner_id` | 双向 | 是 | A |
| status | memories.status | `status` | 双向 | 是 | A |
| version | memories.version | `version` | SQLite→文件 | 是 | B |
| created_at | memories.created_at | `created_at` | 双向 | 是 | B |
| updated_at | memories.updated_at | `updated_at` | 双向 | 是 | B |
| last_accessed_at | memories.last_accessed_at | （不写） | 无 | 否 | C |
| reviewed_at | memories.reviewed_at | `reviewed_at` | 双向 | 条件 | A |
| archived_at | memories.archived_at | `archived_at` | 双向 | 条件 | B |
| expires_at | memories.expires_at | `expires_at` | 双向 | 否 | A |

> 同步器读入 `memory_relations` 时以 `## Relations` 段为准（文件权威）；
> 服务端新增关系（如 derived_from 由派生任务写入）时回写文件段——
> 关系段与库 `memory_relations` 的差异按"段整段重写"收敛（A 类字段，文件胜）。

## 9. 验收自查

- [x] 七类顶层目录 + 三个特殊目录，**统一下划线前缀 `_templates/_archive/_trash`**（§1.1）
- [x] team/project/time 子目录规则，**scope 段使用完整 UUID**（§1.2、§1.3）
- [x] Frontmatter 必填/可选/条件必填 + SQLite 双向映射（§2、§8）；**tags 可选**（§2.1、§8）
- [x] 标题/内链/标签/代码块规则（§3）
- [x] timestamp+slug 命名、Windows/Obsidian 非法字符转义、冲突与重命名策略（§4、§7）
- [x] 两个完整文件示例（§6）
- [x] **字段级权威矩阵（A/B/C 类），消除"Obsidian 权威 vs SQLite 覆盖"双重解释**（§0、§2.5）
- [x] **`_archive/` 参与元数据扫描，archived 文件可重建**（§1.4、§7.4）
- [x] **ops_outbox 补偿协议 + 恢复顺序 + 幂等验证**，替代"同一事务"伪命题（§5.4、§7.3）
