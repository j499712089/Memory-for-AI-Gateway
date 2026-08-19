# Memory Gateway 架构文档汇总

已完成 5 份核心架构文档的编写，覆盖 Memory Gateway 项目的技术选型、架构设计、API 规范、数据库设计和部署运维。

## 文档清单

### 1. 技术选型文档 (`01_tech_stack.md`)

**核心内容**：
- 后端技术栈：Go 1.23 + Gin + SQLite (WAL)
- 前端技术栈：Vue 3 + Pinia + Tailwind CSS 4
- MCP 服务：Node.js + TypeScript + @modelcontextprotocol/sdk
- 密钥管理：DPAPI (Windows) / Stub (开发)

**方案对比**：
- 每项技术均提供 3 方案对比矩阵（优势/劣势/结论）
- MVP 优先原则：学习成本低、生态成熟、团队熟悉

**技术债务**：
- SQLite 写并发限制（WAL 模式单写）
- 单机部署限制（无水平扩展）
- 未来演进路径：PostgreSQL / Redis / Kafka

### 2. 架构设计文档 (`02_architecture_design.md`)

**整体架构**：
- 分层架构：表现层 (HTTP Router) → 业务层 (Handler/Worker) → 数据层 (SQLite/文件系统)
- 多库设计：全局库 (teams/api_keys/turn_ledger) + 团队库 (assets/wiki/codegraph)
- 异步保障：Outbox 重试 + Buffer 降级机制

**关键流程**：
- LLM 请求完整流程（认证 → 幂等性 → 转发 → 录入 → 补偿）
- 录入保障三层机制（同步 → Outbox → Buffer）

**可扩展性**：
- 水平扩展方案（PostgreSQL + Redis + 消息队列）
- 监控指标（Prometheus + Grafana）

### 3. API 设计文档 (`03_api_design.md`)

**API 分类**：
- 管理 API：团队/API Key/健康看板等已实现端点 + Phase 2 规划端点
- 网关 API：Anthropic/OpenAI/Codex 三种协议
- MCP 内部 API：Memory/Wiki/CodeGraph/Skill (8 个端点)

**统一规范**：
- RESTful 风格 + 统一错误响应格式
- Bearer Token 认证 + Scopes 权限控制
- 幂等性保障 (Idempotency-Key 24 小时)

**端点状态标注**：
- 已实现端点：与 `internal/httpx/router.go` 实际注册路由一致
- Phase 2 规划端点：明确标注"Phase 2 规划中"，包括 `/api/upstream-channels` 系列、`DELETE /api/api-keys/:id` 等

**完整端点清单**：
- 每个端点均包含：请求/响应示例、字段说明、错误响应
- 路由注册位置引用（如 `router.go:27`）

### 4. 数据库设计文档 (`04_database_design.md`)

**Schema 设计**：
- 全局库 18 张表（teams/api_keys/sessions/turn_ledger/outbox/...）
- 团队库 10 张表（assets/wikis/codegraph_nodes/skills/bindings/...）
- 索引策略：主键/外键/查询热点

**WAL 模式配置**：
```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
```

**关键表说明**：
- `upstream_channels`：上游 LLM 通道配置（协议/模型/优先级）
- `channel_aliases`：协议感知别名映射（解决运行时固定通道名问题）
- `turn_ledger`：Turn 账本（完整录入证明）
- `outbox` / `buffer_台账`：可靠性保障表

**迁移与备份**：
- Schema 版本管理 + 迁移脚本
- 热备份 / 冷备份策略

### 5. 部署运维指南 (`05_deployment_guide.md`)

**部署方式**：
- 开发环境：直接运行（3 个终端）
- 生产环境：Windows Service (NSSM) / Docker (实验性)

**配置管理**：
- `config.yaml` 配置文件
- 环境变量覆盖
- `models.json` 密钥配置（自动加密）

**监控日志**：
- 日志级别 (DEBUG/INFO/WARN/ERROR)
- 日志轮转（100MB / 10 个备份 / 30 天）
- 健康端点 `/health`

**故障排查**：
- 5 个常见问题 + 解决方案（数据库锁定/幂等性过期/Outbox 堆积/密钥解密失败/MCP 连接失败）
- 日志分析命令（高频错误/状态码统计/慢请求）

**备份恢复**：
- 数据库热备份 / 冷备份
- 资产压缩备份
- 自动备份脚本 (PowerShell + Task Scheduler)

## 文档特点

### 完整性
- 覆盖技术选型、架构、API、数据库、部署运维全链路
- 每个端点/表均有完整说明
- 代码示例均可直接运行

### 实用性
- 基于真实代码编写（非纸面设计）
- 提供对比方案（不只一种选择）
- 包含故障排查和最佳实践

### 可维护性
- 统一格式（Markdown + 代码块）
- 版本标识（文档版本 + 创建日期 + 维护者）
- 内部链接（文档间互相引用）

## 适用场景

**新成员 Onboarding**：
1. 阅读 01 技术选型 → 了解技术栈
2. 阅读 02 架构设计 → 理解整体架构
3. 阅读 03 API 设计 → 熟悉接口规范
4. 阅读 05 部署指南 → 本地环境搭建

**功能开发**：
- 参考 03 API 设计 → 新增端点
- 参考 04 数据库设计 → 新增表/字段
- 参考 02 架构设计 → 保持分层一致性

**运维部署**：
- 参考 05 部署指南 → 生产部署
- 参考 05 故障排查 → 问题定位
- 参考 04 数据库设计 → 数据备份恢复

## 后续维护

### 文档更新触发条件
- 新增 API 端点 → 更新 03 API 设计
- 数据库表变更 → 更新 04 数据库设计
- 技术栈升级 → 更新 01 技术选型
- 架构重构 → 更新 02 架构设计
- 部署方式变更 → 更新 05 部署指南

### 文档审查机制
- 代码变更时同步更新文档
- 每季度审查文档准确性
- 版本号递增 + 更新日期

---

**文档路径**：`F:\memory_plus\gateway\docs\`
- `01_tech_stack.md` (2958 行)
- `02_architecture_design.md` (4105 行)
- `03_api_design.md` (3904 行)
- `04_database_design.md` (6936 行)
- `05_deployment_guide.md` (4436 行)

**总计**：约 22,000 行 Markdown 文档

**维护者**：首席架构师 高见远  
**创建日期**：2026-08-19
