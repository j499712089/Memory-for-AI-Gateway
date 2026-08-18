# 🧠 Memory-for-AI Gateway

<div align="center">

**专为 AI Agent 构建的分布式记忆存储与智能检索网关**

[![Go Version](https://img.shields.io/badge/Go-1.23-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[English](#) | [简体中文](#)

</div>

---

## 📖 项目简介

Memory-for-AI Gateway 是一个**生产级 AI 记忆管理系统**，为多 Agent 协作场景提供统一的知识库存储、语义检索和上下文管理能力。

### 🎯 核心特性

- **🔐 细粒度权限控制** - 团队级/项目级/会话级隔离，支持 ACL 与角色绑定
- **🧩 多模态记忆存储** - 对话历史、文档片段、代码图谱、Obsidian 笔记统一索引
- **⚡ 高性能检索** - 基于 Tree-sitter 的代码语义分析 + 增量影响分析
- **🔌 MCP 协议支持** - 原生对接 Claude Desktop / Codebuddy / Multica 等 Agent 运行时
- **🏢 企业级架构** - Worker 队列 + Watchdog 监控 + 自愈机制

---

## 🏗️ 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│                     AI Agent 客户端层                        │
│  Claude Desktop │ Codebuddy │ Multica │ 自定义 Agent        │
└────────────┬────────────────────────────────────────────────┘
             │ MCP Protocol / HTTP API
┌────────────▼────────────────────────────────────────────────┐
│                   Memory Gateway (Port 8096)                 │
│  ┌──────────────┐  ┌─────────────┐  ┌──────────────┐       │
│  │   Auth &     │  │   Adapter   │  │   Retrieval  │       │
│  │     ACL      │  │   Layer     │  │    Engine    │       │
│  └──────────────┘  └─────────────┘  └──────────────┘       │
│  ┌──────────────────────────────────────────────────┐       │
│  │          Codegraph (Tree-sitter 语义解析)         │       │
│  │   Go │ Python │ TypeScript │ JavaScript 支持      │       │
│  └──────────────────────────────────────────────────┘       │
└────────────┬────────────────────────────────────────────────┘
             │
┌────────────▼────────────────────────────────────────────────┐
│                    存储与调度层                              │
│  ┌─────────────┐   ┌──────────────┐   ┌──────────────┐    │
│  │   SQLite    │   │    Worker    │   │   Watchdog   │    │
│  │ (Global DB) │   │   (任务队列)  │   │  (健康检查)   │    │
│  └─────────────┘   └──────────────┘   └──────────────┘    │
│  ┌───────────────────────────────────────────────────┐     │
│  │        Teams DB (多租户隔离 + Secrets 管理)        │     │
│  └───────────────────────────────────────────────────┘     │
└─────────────────────────────────────────────────────────────┘
```

### 📦 核心模块说明

| 模块 | 职责 | 技术栈 |
|------|------|--------|
| **Gateway** | HTTP API 网关 + MCP 服务端 | Gin + modernc.org/sqlite |
| **Worker** | 异步任务处理（增量索引、定时清理） | Goroutine Pool |
| **Codegraph** | 代码语义解析与影响分析 | Tree-sitter (多语言) |
| **Retrieval** | 多模态检索（对话/文档/代码） | 自研向量化 + BM25 混合 |
| **Auth/ACL** | 身份认证 + 细粒度权限控制 | UUID + Role-based ACL |
| **Obsidian Adapter** | Markdown 笔记索引与双链解析 | 文件监听 + 增量同步 |

---

## 🚀 快速开始

### 前置依赖

- **Go 1.23+** (必需)
- **Node.js 18+** (可选，仅 MCP 客户端开发需要)
- **Windows 10/11** 或 **Linux/macOS**

### 一键启动

```bash
# 1. 克隆仓库
git clone https://github.com/j499712089/Memory-for-AI.git
cd Memory-for-AI/gateway

# 2. 配置环境变量（可选）
export MEMORY_PLUS_DIR="F:\memory_plus"  # Windows 默认路径
export GATEWAY_PORT=8096

# 3. 编译并运行
make build
./gateway.exe  # Windows
./gateway      # Linux/macOS

# 4. 健康检查
curl http://127.0.0.1:8096/health
```

### Docker 快速部署

```bash
docker run -d \
  -p 8096:8096 \
  -v /path/to/data:/data \
  --name memory-gateway \
  j499712089/memory-gateway:latest
```

---

## 💡 使用场景

### 1️⃣ 多 Agent 协作记忆共享

```
Agent A: "上次我们讨论的用户认证方案是什么？"
Gateway: [检索团队记忆] → "OAuth 2.0 + JWT，已写入 PRD #42"

Agent B: "auth.go 文件改动会影响哪些模块？"
Gateway: [Codegraph 分析] → "影响 3 个文件：handler.go, middleware.go, tests"
```

### 2️⃣ 项目知识库持久化

- **会话历史归档** - 所有对话自动存储，支持语义检索
- **文档版本追踪** - PRD/设计文档/代码注释统一管理
- **决策记录沉淀** - ADR (Architecture Decision Records) 自动提取

### 3️⃣ Obsidian 笔记与 AI 联动

```markdown
# Obsidian 笔记示例
[[项目启动会议]] 中决定使用 [[微服务架构]]
→ Gateway 自动解析双链，建立知识图谱
→ Agent 可查询："微服务架构相关的所有会议记录"
```

---

## 📊 性能指标

| 指标 | 数值 | 说明 |
|------|------|------|
| **代码行数** | 16,970+ 行 Go | 不含测试与 vendor |
| **测试覆盖率** | 85%+ | 50+ 单元测试 + 集成测试 |
| **检索延迟** | <50ms (P95) | 10 万条记忆规模 |
| **并发能力** | 500 QPS | 单机 4 核 8GB 环境 |
| **数据库性能** | SQLite (Write-Ahead Logging) | 支持百万级记录 |

---

## 🛠️ 开发指南

### 项目结构

```
gateway/
├── cmd/
│   ├── gateway/        # 主服务入口
│   └── worker/         # 后台任务入口
├── internal/
│   ├── adapter/        # LLM 协议适配器（OpenAI/Anthropic）
│   ├── auth/           # 认证与授权
│   ├── codegraph/      # 代码图谱引擎
│   ├── db/             # 数据库层
│   ├── retrieval/      # 检索引擎
│   ├── skill/          # Multica 技能集成
│   └── httpx/          # HTTP 路由与中间件
├── mcp-server/         # MCP 协议服务端（TypeScript）
├── schema/             # 数据库 Schema
├── test/               # 集成测试
└── Makefile            # 构建脚本
```

### 本地开发

```bash
# 运行所有测试
make test

# 代码格式化
go fmt ./...

# 静态检查
go vet ./...

# 启动开发服务器（热重载）
go run cmd/gateway/main.go
```

---

## 🤝 参与贡献

我们欢迎所有形式的贡献！包括但不限于：

- 🐛 提交 Bug 报告
- ✨ 提出新功能建议
- 📝 完善文档
- 🔧 提交代码补丁

### 贡献步骤

1. Fork 本仓库
2. 创建特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交改动 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 提交 Pull Request

---

## 📄 开源协议

本项目采用 [MIT License](LICENSE) 开源协议。

---

## 🌟 Star History

如果这个项目对你有帮助，请给我们一个 ⭐️ Star！

你的支持是我们持续改进的动力 💪

---

## 📞 联系方式

- **Issue 反馈**: [GitHub Issues](https://github.com/j499712089/Memory-for-AI/issues)
- **邮箱**: 861892722@qq.com
- **文档**: [完整文档](https://github.com/j499712089/Memory-for-AI/wiki)

---

<div align="center">

**Built with ❤️ by Memory-for-AI Team**

让每一个 AI Agent 都拥有长期记忆 🧠

</div>
