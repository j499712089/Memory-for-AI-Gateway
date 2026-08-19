# 技术选型文档

## 项目概述

Memory Gateway 是一个多租户 LLM 请求网关系统，提供统一的 API 网关、录入保障、MCP 服务和管理面板。

## 技术栈选型

### 1. 后端核心 - Go 1.23

**选型理由：**
- 高并发性能：原生 goroutine 支持，适合网关场景的高并发请求处理
- 简单部署：单一可执行文件，无运行时依赖
- 丰富生态：Gin、database/sql 等成熟框架
- 内存安全：垃圾回收，避免手动内存管理
- 团队熟悉：Go 是主力技术栈

**替代方案对比：**

| 方案 | 优势 | 劣势 | 结论 |
|------|------|------|------|
| **Go (已选)** | 高并发、简单部署、团队熟悉 | 泛型支持较晚期 | ✅ 最优选择 |
| Node.js | 生态丰富、异步 I/O | 单线程瓶颈、内存占用高 | ❌ 不适合高并发网关 |
| Python | 快速开发、ML 生态好 | GIL 限制并发、部署复杂 | ❌ 性能不足 |
| Rust | 极致性能、内存安全 | 学习曲线陡峭、开发慢 | ❌ MVP 期不合适 |

### 2. Web 框架 - Gin v1.10

**选型理由：**
- 高性能：基于 httprouter，性能优于标准库
- 易用性：中间件机制清晰，路由定义直观
- 社区成熟：Star 78k+，问题解决快
- 轻量级：核心库小，启动快

**替代方案对比：**

| 方案 | 优势 | 劣势 | 结论 |
|------|------|------|------|
| **Gin (已选)** | 高性能、易用、社区成熟 | 缺少官方文档规范 | ✅ 最优选择 |
| Echo | 性能接近、文档更好 | 社区比 Gin 小 | ⚠️ 可考虑 |
| Fiber | Go 版 Express、快速 | 底层 fasthttp 不兼容标准库 | ❌ 风险高 |
| net/http | 标准库、零依赖 | 缺少中间件、路由弱 | ❌ 开发效率低 |

### 3. 数据库 - SQLite 3 (WAL 模式)

**选型理由：**
- 零配置：嵌入式数据库，无需独立服务
- 高可靠：ACID 事务，WAL 模式支持并发读写
- 低延迟：本地文件访问，无网络开销
- 轻量部署：单文件数据库，便于备份迁移
- MVP 适配：团队数据量小（< 1TB），单机足够

**WAL 模式配置：**
```sql
PRAGMA journal_mode = WAL;       -- 并发读写
PRAGMA foreign_keys = ON;        -- 外键约束
PRAGMA busy_timeout = 5000;      -- 写锁等待 5s
PRAGMA synchronous = NORMAL;     -- WAL 下安全且高效
PRAGMA wal_autocheckpoint = 1000;
```

**替代方案对比：**

| 方案 | 优势 | 劣势 | 结论 |
|------|------|------|------|
| **SQLite (已选)** | 零配置、高可靠、低延迟 | 单机限制、写并发有限 | ✅ MVP 首选 |
| PostgreSQL | 高并发、丰富功能 | 需独立服务、部署复杂 | ❌ MVP 过度设计 |
| MySQL | 成熟生态、分布式好 | 需独立服务、资源占用高 | ❌ MVP 过度设计 |
| Redis | 极致性能 | 内存数据库、持久化弱 | ❌ 不适合主库 |

### 4. 前端框架 - Vue 3 + TypeScript

**选型理由：**
- 渐进式：核心库轻量，按需引入功能
- Composition API：逻辑复用好，TypeScript 支持佳
- 性能优异：虚拟 DOM + 编译优化
- 生态成熟：Vue Router、Pinia 配套完善
- 团队熟悉：主力前端框架

**替代方案对比：**

| 方案 | 优势 | 劣势 | 结论 |
|------|------|------|------|
| **Vue 3 (已选)** | 渐进式、性能好、团队熟悉 | 企业级组件库不如 React | ✅ 最优选择 |
| React | 生态最大、组件库丰富 | JSX 学习成本、状态管理复杂 | ⚠️ 可考虑 |
| Svelte | 编译时优化、无虚拟 DOM | 生态小、招聘难 | ❌ 风险高 |
| Alpine.js | 极轻量、jQuery 式 | 功能弱、不适合复杂应用 | ❌ 不满足需求 |

### 5. 状态管理 - Pinia

**选型理由：**
- Vue 官方推荐：替代 Vuex，设计更现代
- TypeScript 原生支持：类型推断完善
- DevTools 集成：调试体验好
- 模块化：按功能拆分 store，清晰易维护

**替代方案：**
- Vuex：老牌方案，但 Pinia 已是官方推荐
- 原生 Composition API：简单场景可用，但缺少持久化、DevTools 等特性

### 6. 构建工具 - Vite

**选型理由：**
- 开发体验：HMR 极快，秒级启动
- 生产优化：基于 Rollup，Tree Shaking 好
- Vue 官方推荐：Vue 作者开发
- ESM 原生支持：现代浏览器直接运行

**替代方案：**
- Webpack：成熟但慢，配置复杂
- Parcel：零配置但生态小
- esbuild：极快但插件生态弱

### 7. UI 样式 - Tailwind CSS 4

**选型理由：**
- 原子化 CSS：快速开发，样式可复用
- JIT 编译：按需生成，体积小
- 深色模式：原生支持，切换简单
- 设计系统友好：配置化颜色、间距等 token

**替代方案对比：**

| 方案 | 优势 | 劣势 | 结论 |
|------|------|------|------|
| **Tailwind (已选)** | 快速开发、体积小、设计系统友好 | 类名冗长 | ✅ 最优选择 |
| CSS-in-JS | 动态样式好 | 运行时开销、SSR 复杂 | ❌ 性能差 |
| Bootstrap | 组件丰富 | 样式固定、定制难 | ❌ 不够灵活 |
| 原生 CSS | 零依赖 | 开发慢、维护难 | ❌ 效率低 |

### 8. MCP 服务 - Node.js + TypeScript

**选型理由：**
- 官方 SDK：@modelcontextprotocol/sdk 仅 Node.js 版本
- 异步 I/O：适合 MCP 的 stdio/HTTP 双协议
- TypeScript：类型安全，与前端统一技术栈
- 轻量级：MCP 服务逻辑简单，Node.js 足够

**替代方案：**
- 纯 Go 实现：需自行实现 MCP 协议，成本高
- Python：asyncio 可用，但部署不如 Node.js 简单

### 9. 密钥管理 - DPAPI (Windows) / Stub (开发)

**选型理由：**
- 零依赖：DPAPI 是 Windows 内置加密 API
- 安全性：密钥存储在系统保护的存储中
- 简单部署：无需独立密钥服务
- Stub 模式：开发环境使用明文文件，快速调试

**替代方案：**
- HashiCorp Vault：功能强大，但 MVP 过度设计
- AWS Secrets Manager：云服务依赖，不适合本地部署
- 环境变量：不安全，易泄露

## 架构分层

### 三层架构

```
┌─────────────────────────────────────────────────────────────┐
│ 表现层 (Presentation Layer)                                  │
│ - HTTP Router (Gin)                                          │
│ - Request/Response 转换                                       │
│ - 中间件 (Auth/Idempotency/Logging)                         │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ 业务层 (Business Layer)                                      │
│ - Handler: admin/gateway/health/mcp                          │
│ - Worker: 异步任务、录入补偿                                 │
│ - Secrets: 密钥管理                                           │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ 数据层 (Data Layer)                                          │
│ - SQLite 全局库 (teams/api_keys/sessions/turn_ledger)       │
│ - SQLite 团队库 (assets/wiki/codegraph/skill)               │
│ - 文件系统 (L0/L1/L2/L3 资产正文)                            │
└─────────────────────────────────────────────────────────────┘
```

## 依赖版本锁定

### Go 依赖 (go.mod)
```go
require (
    github.com/gin-gonic/gin v1.10.0
    github.com/mattn/go-sqlite3 v1.14.24
    github.com/google/uuid v1.6.0
)
```

### 前端依赖 (web-panel/package.json)
```json
{
  "dependencies": {
    "pinia": "^4.0.3",
    "vue": "^3.5.41",
    "vue-router": "^5.2.0"
  },
  "devDependencies": {
    "@tailwindcss/vite": "^4.3.3",
    "@vitejs/plugin-vue": "^6.0.8",
    "tailwindcss": "^4.3.3",
    "typescript": "^5.9.3",
    "vite": "^8.2.1"
  }
}
```

### MCP 服务依赖 (mcp-server/package.json)
```json
{
  "dependencies": {
    "@modelcontextprotocol/sdk": "^1.30.0",
    "express": "^4.19.2",
    "zod": "^4.4.3"
  }
}
```

## 技术债务与未来演进

### 已知限制
1. **SQLite 写并发**：WAL 模式下单写，高并发写场景需考虑 PostgreSQL
2. **单机部署**：当前架构不支持水平扩展，需引入分布式锁/数据库
3. **密钥管理**：DPAPI 仅 Windows，Linux 需 Stub 或云服务

### 未来演进路径
1. **数据库**：团队数据量 > 100GB 或写 QPS > 500 时，迁移 PostgreSQL
2. **缓存层**：高频读取 (如 API Key 校验) 引入 Redis
3. **消息队列**：异步任务量大时，用 RabbitMQ/Kafka 替代 SQLite task_queue
4. **监控告警**：接入 Prometheus + Grafana

## 决策记录

- ADR-001: Go + Gin + SQLite 作为 MVP 技术栈 (已选定)
- ADR-002: Vue 3 + Tailwind CSS 构建管理面板 (已选定)
- ADR-003: WAL 模式保障并发读写 (已选定)
- ADR-004: DPAPI 作为 Windows 密钥管理方案 (已选定)
- ADR-005: MCP 服务使用官方 Node.js SDK (已选定)

---

**文档版本**: 1.0  
**创建日期**: 2026-08-19  
**维护者**: 首席架构师 高见远
