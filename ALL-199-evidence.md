## ALL-199 ops_outbox 崩溃注入测试 — 证据

合同来源：ALL-69 **v2.1** §5.4 / ADR-004 **v2.2** D4
测试文件：`test_sync_outbox_contract.py`（821 行，仓库根目录，随合同文档一起提交）
结果：**134 断言全部通过，exit 0，连续两次运行断言判定完全一致**（输出仅临时目录随机后缀不同，归一化后逐字节相同）

### 五项必修项落实

**1. 真实驱动器，不是预置状态**
`Engine` 是按 §5.4 逐步实现的状态机，每个断言的终态都由驱动器跑出来，不再手工 seed。§6 专门用驱动器产出的 `_archive` 文件反推 memory_id / status / source_path / 正文，确认 `archived_at` 由驱动器写入（6.3）而非预置（6.4–6.7）。

**2. DDL 取自交付物，不是内联副本**
本次交付（ALL-201 R2-1）只提交三样东西：`docs/ALL-69-obsidian-file-structure-spec.md`、`docs/decisions/ADR-004-obsidian-sqlite-sync.md` 和 `test_sync_outbox_contract.py`，其中没有任何迁移文件或独立 `.sql` 来承载 `ops_outbox` —— §5.4 的 DDL 块就是权威来源。因此测试在运行时正则解析 ALL-69 spec 里的 `ops_outbox(...)` 并直接 `executescript`（0.0），解析出的 10 列与 phase/op_type CHECK 约束均为实测生效（0.1–0.6，0.3/0.4 用 INSERT probe 证明 op_type 枚举是 DB 层约束而非行尾注释）。新增 0.0 断言锁住这条来源，防止后续重构悄悄退回手抄。

**3. 崩溃边界矩阵覆盖全部 6 种 op_type**
- create：`crash_at ∈ {None,1,2,3,4,5}`（1=db_txn，2=temp_write，3=rename，4=writeback，5=done）— 12 断言
- update：`{None,1,2,3,4,5}`，与 create 共享 `run_create_or_update`，因此 4/5 两个边界也必须注入（ALL-201 R2-2）— 12 断言
- move / archive / trash：每种 `{None,1,2,3,4,5}` — 54 断言
- purge：`{None,1,2,3}` — 20 断言
每个边界都验证「收敛 + 重放幂等」两条（update 的幂等检查为 version 不再 bump），重放幂等是防止恢复逻辑二次写坏的关键。

**4. 步骤顺序按 op_type 分别断言**
create 为 DB 先行 `['db_txn','temp_write','rename','writeback','done']`（0.7）；move 为文件先行、旧路径最后删 `['temp_write','rename','db_txn','unlink_old','done']`（0.8）。0.9 单独锁 §5.4 硬规则：目标文件存在前绝不更新 `source_path`。

**5. RED 参照系**
§7 用 `LegacyEngine` 复现 ALL-196 的 write→rename→commit 顺序，证明它违反合同：顺序与 §5.4 row 1 相反（7.1/7.2）、崩溃后留下无 DB 行的孤儿文件（7.3）、`sync_state` 为空（7.4）、且旧引擎根本没有 trash/update/purge 的驱动器（7.5，ALL-201 R2-3a——原 7.5 是两个字的字面量比较，永真，已替换成真实缺口断言；7.6 同类对照：合同引擎能驱动全部 6 种 op_type）。这一节是防回归的对照组，不是待修项。

### 其他覆盖

§5 覆盖冲突、回滚、跨文件系统与失败终态：hash 不一致时文件原样保留为 `.conflict-<hash8>.md` 且 op 转 failed、写冲突事件（5.1–5.4）；DB 事务中止不残留半行、outbox 保持 pending、恢复后收敛（5.5–5.8）；EXDEV 下 rename 失败由 copy+unlink 补偿且旧路径确实移除（5.9/5.10），`attempt_count` 递增到 3 后 phase 转 `failed` 并写出 `sync_failure` 事件（5.11–5.13）。§4 确认 purge 后文件消失、行转 `purged`、事件保留。§6 从真实驱动器产生的 `_archive` 文件反推全字段，证明归档元数据不是 seed 的。

### 卫生

临时目录经 `atexit` 清理，运行后 `all199_*` 残留为 0。完整运行输出见 `.all199/run_r3.txt`（随证据同目录）。
