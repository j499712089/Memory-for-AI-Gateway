# ADR-004: Obsidian ↔ SQLite 同步策略 — 字段级权威矩阵 + outbox 补偿收敛

> ADR ID: ADR-004-obsidian-sqlite-sync · 日期: 2026-08-24 · 决策者: 首席架构师-高见远
> 状态: **Accepted（2026-08-24）** · 新主题，无被取代对象（上游无对应 ADR）
> v2.2 修订（ALL-201 R2-3）：Decision 4 增记 `op_type` DB 层 CHECK 约束取舍（配套
> ALL-69 v2.1 §5.4）。
> v2.1 修订（ALL-196 Blocking C）：与 ALL-69 v2.0 同步合同——字段级权威矩阵替代
> "文件为源/SQLite 可覆盖"双重表述；`_archive` 参与元数据扫描；outbox 协议替代
> "单事务同步"表述；sidecar 持久格式落定为 SQLite `sync_state` 表。

## Title

Obsidian Markdown 与 SQLite Registry 的同步策略：**字段级权威矩阵**（A 类文件权威 / B 类库权威 /
C 类库私有）+ **ops_outbox 补偿协议**保证跨域一致；双向收敛并内置冲突仲裁。

## Status

Accepted（2026-08-24）。本 ADR 为 ALL-72 交付物（补交），无上游同主题 ADR；与 ALL-69 v2.1（文件规范）配套
——ALL-69 定义文件格式,本 ADR 定义文件 ↔ 库的同步协议，**口径以 ALL-69 v2.1 为准**。

## Context

- 架构前提（ALL-66）: Obsidian 仓库 = 人可读、git 可版本、自然可迁移；SQLite = 结构化检索。
- 需求: 用户在 Obsidian 里直接编辑（改正文/标签/关系），库要跟进；服务端产生的元数据
  （chunk、embedding、version、event）只属于 SQLite，不写回文件。
- 风险: 双向不同步 → 检索陈旧、或覆盖用户编辑（最坏丢数据）。任何同步必须
  **不覆盖未经合并的用户改动**。
- 约束: 进程可能任意时刻退出（任务终止即交易结束，Multica 无后台唤醒），
  同步必须**幂等、可续跑、崩溃安全**；内容哈希 + 文件 mtime 双通道检测变更。
- **跨域事务事实（v2.1 修正）**: 文件系统与 SQLite **不存在**共同 ACID 事务管理器，
  "文件↔库在同一事务提交"在物理上不可能。一致性只能靠补偿协议（Decision 4）。

## Decision

1. **权威模型（取代 v1 的"文件系统是 source of truth / SQLite 是派生索引"单一表述）**
   — 按字段分派，无双重解释（ALL-69 §0）：

   | 类 | 字段 | 冲突时谁胜 |
   |---|---|---|
   | A · 文件权威 | `title`、正文、`tags`、`## Relations`、`category`、`language`、`summary`、`status`、`reviewed_by/at`、`expires_at`、scope 字段、`owner_id`、`source_type`、`created_by`、`importance`、`confidence` | **文件胜**（用户编辑不被覆盖，回写库） |
   | B · 库权威 | `memory_id`、`version`、`slug`、`source_path`、`created_at`、`updated_at`、`archived_at` | **库胜**（文件侧由库回写） |
   | C · 库私有 | `content_hash`、`chunk_count`、`embedding_model`、向量、`last_accessed_at`、versions/events/scopes/chunks/fts 全部行 | 不存在于文件，无冲突 |

   - **"SQLite 可覆盖文件"的表述删除**：SQLite 只回写 B 类键与 C 类**衍生物**（不落文件）；
     A 类字段永远文件为准。用户编辑永不静默丢失。
   - **SQLite 可由文件全量重建**（A 类完整可读 + B 类派生 + C 类重算，ALL-69 §7.4）。

2. **检测变更（双通道）**
   - 通道 A: 文件 mtime + size 变化 → 视为"可能变更"，进入 diff 队列。
   - 通道 B: 正文 `content_hash = SHA256(canonicalize(content))`，写入 `memories.content_hash`；
     diff 时重算比对，**hash 不变则跳过分块/向量重算**（节省 90% 无关计算）。

3. **同步协议（幂等）**
   ```
   sync_once():
     files     = walk(vault, "*.md")        # 见扫描范围（下方表）
     changed   = files where (mtime,size) != sync_state 或 hash != content_hash
     deleted   = sync_state 路径 不存在于 files（且非 *_trash/_templates 管辖）
     for f in changed:  parse(f) → 按权威矩阵 upsert memories (+ chunks, tags, scopes, relations)
     for d in deleted:  走 archive（软删，保留 history；文件体移入 _archive/）
     commit; 更新 sync_state（path → memory_id, mtime, size, content_hash, last_indexed_at）
   ```
   扫描范围（与 ALL-69 §5.2 一致）：

   | 路径 | 处理 |
   |---|---|
   | 7 个类型目录 | 正常 upsert |
   | `_archive/` | **解析元数据**（只读）：校准/重建 archived 行；不重算 chunk/embedding |
   | `_trash/` | 跳过（物理生命周期 ALL-69 §7.2） |
   | `_templates/` | 跳过（不上行） |
   | `.obsidian/`、`.git/`、`.multica/` | 跳过 |

4. **跨域一致性: ops_outbox 补偿协议**（替代 v1 Decision 6"同步在一个事务内完成"）
   - 每个文件写操作 = SQLite `ops_outbox` 行 + 库行变更（同一 SQLite 事务）→
     临时文件 + 原子 rename → B 类回写 → outbox done。
   - **崩溃恢复顺序**（任意崩溃点幂等）：见 ALL-69 §5.4 表——
     (a) outbox pending 且文件未动 → 重做文件写入（目标存在且 hash 一致则跳过）；
     (b) 文件已落、库未推进 → 校验 hash 后推进库；
     (c) 库已更新、旧文件未删 → 删旧文件（属本 memory_id 才删）；
     (d) outbox done 幂等确认。连续 3 次失败 → phase=failed + 事件，人工/Agent 复核。
   - **move/rename 顺序钉死**：目标路径先生效 → 库 `source_path` 跟进 → 旧路径清删；
     **禁止先更新 `source_path` 再移动文件**（v1 表述，崩溃即悬空路径）。
   - **op_type / phase 双约束（v2.2，ALL-201 R2-3）**：两列均在 DB 层用 CHECK 钉死取值集
     （见 ALL-69 v2.1 §5.4）。`op_type` 原先只有注释、无约束，非法值被静默接受；而恢复
     协议按 `op_type` 分派驱动器，未知取值会把插入期错误推迟成恢复期崩溃。取舍：宁可在
     写入点 fail-fast，也不让恢复协议面对无法分派的 outbox 行。
   - **恢复协议必须覆盖**：DB commit 前/后、文件 rename 前/后崩溃，
     均可重放至一致状态；验证见 ALL-196 附录 `test_sync_outbox.py`（故障注入矩阵全绿）。

5. **冲突仲裁（文件 vs 库，按权威类）**
   - A 类字段两侧都变且不同 → **文件胜**：文件原样保留为新文件 `<原名>.conflict-<hash8>.md`
     （绝不静默覆盖任一版本），库行冻结在变更前版本，通知 owner 人工/Agent 复核。
   - B 类字段冲突（如 version 两侧不等）→ 库胜，文件回写。
   - 删除: 用户删文件 → 走 archive（不删行）；恢复文件 → 复活（status 按 Frontmatter）。
6. **命名与路径**（与 ALL-69 §4/§1 一致）
   - `_templates/_archive/_trash` 为统一下划线前缀特殊目录；`_archive` 参与元数据扫描。
   - scope 段用**完整 UUID**（role/team/project 全量 id，可逆、无碰撞），
     不做 8 位前缀映射；同步器随附碰撞测试（ALL-69 §1.2）。
   - SQLite 侧 `memories.source_path` 为唯一路径键（`UNIQUE` 下 partial index 处理软删复活）。
7. **sidecar 持久格式（v2.1 落定，替代 v1 未定义的"索引 sidecar"）**
   - 同步状态落 **SQLite 表 `sync_state`**（不是文件 sidecar）:
     ```sql
     CREATE TABLE sync_state (
       path TEXT PRIMARY KEY,             -- source_path
       memory_id TEXT NOT NULL REFERENCES memories(memory_id),
       mtime INTEGER NOT NULL, size INTEGER NOT NULL,
       content_hash TEXT NOT NULL,
       last_indexed_at TEXT NOT NULL DEFAULT (datetime('now','utc'))
     );
     ```
   - "库有未提交修改"的**判定定义** = 存在 `ops_outbox` phase ∈ {pending, committed, failed}
     的行，或 `sync_state.last_indexed_at < memories.updated_at`；
     该状态下禁止执行任何会覆盖文件 A 类字段的回写。
   - 冲突副本（`.conflict-<hash8>.md`）**不删、不合并**，保留至 owner 处理；
     不再以"system_written"或 mtime 跳过标记作为**持久**格式（运行期去抖可用，持久状态以 sync_state + outbox 为准）。

## Consequences

- 正面: 用户编辑 Obsidian 后，下一次 sync 自动进入库（30 s 轮询或文件事件触发）。
- 正面: git 可作为文件级版本备份；SQLite 可随时 `rebuild` 从文件重来（ALL-69 §7.4）。
- 正面: 软删除: 用户删文件不会丢库行（走 archive），可恢复。
- 风险: 双通道 hash 计算有小成本（大仓库首次全量较慢；增量只读变更文件）。
- 风险: 双向同步必须防"服务端写回触发再同步"的循环: 运行期用 B 类键变更 + mtime 跳过一槽
  去抖；持久状态以 sync_state 为准。
- 风险: 首次全量导入（历史 453 行）会重算 embedding: 建议分批 200 行/事务，避免长事务。
- 风险: outbox 积累 backlog（高频小写）: 每次 sync 前先重放 ≤ 100 条，
  超限告警（写 `sync_backlog` 事件），防止恢复窗口无限拉长。

## Alternatives Considered

| 方案 | 否决原因 |
|---|---|
| 仅文件→库（单向） | 无法承载服务端生成的 B 类元数据写回（version/archived_at）；无法自动修复损坏文件 |
| 仅库→文件（单向） | 用户 Obsidian 改动会被忽略，检索与文件脱节 |
| 双向全量覆盖 | 数据丢失风险，绝不采纳 |
| 文件作索引、SQLite 作源 | 违背"文件是用户编辑主入口"的事实；且结构化查询依赖 SQLite |
| 单事务同步（v1） | 跨域无共同事务管理器，物理不可能；**被 outbox 补偿协议取代** |
| 字段级权威矩阵 + outbox（本方案） | **采用**——文件源覆盖用户编辑面、库源覆盖系统面、补偿覆盖崩溃面 |

## 一致性声明

- 与 ALL-69 v2.1（权威矩阵、特殊目录、完整 UUID、outbox 协议）**逐项同口径**；
  本文档 v1 的冲突语义（"文件 mtime 更新则文件胜"）已被字段级矩阵取代，以本版为准。
- 上游 ALL-66 附件仅确认"文件为源"倾向；本文档为形式化落地，含防覆盖/防循环/删除语义。
- 与 ALL-70 §2（filter 阶段 status 过滤）衔接: archived 记忆默认不跨库检索，
  与同步软删/归档语义一致（`_archive` 元数据扫描不影响检索，检索按 §2.2 状态白名单）。
