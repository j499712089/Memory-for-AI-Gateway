# 🧠 Memory-for-AI Gateway

<div align="center">

**Distributed Memory Storage and Intelligent Retrieval Gateway for AI Agents**

**专为 AI Agent 构建的分布式记忆存储与智能检索网关**

[![Go Version](https://img.shields.io/badge/Go-1.23-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

[English](#english) | [简体中文](#简体中文)

</div>

---

# English

## 📖 Introduction

Memory-for-AI Gateway is a **production-grade AI memory management system** that provides unified knowledge base storage, semantic retrieval, and context management capabilities for multi-agent collaboration scenarios.

### 🎯 Core Features

- **🔐 Fine-grained Access Control** - Team/Project/Session-level isolation with ACL and role binding
- **🧩 Multi-modal Memory Storage** - Unified indexing for conversation history, document fragments, code graphs, and Obsidian notes
- **⚡ High-performance Retrieval** - Tree-sitter-based code semantic analysis with incremental impact analysis
- **🔌 MCP Protocol Support** - Native integration with Claude Desktop, Codebuddy, Multica, and other agent runtimes
- **🏢 Enterprise Architecture** - Worker queue + Watchdog monitoring + self-healing mechanisms

---

## 🏗️ System Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│                      AI Agent Client Layer                        │
│   Claude Desktop  │  Codebuddy  │  Multica  │  Custom Agents    │
└─────────────────┬────────────────────────────────────────────────┘
                  │ MCP Protocol / HTTP API
┌─────────────────▼────────────────────────────────────────────────┐
│                  Memory Gateway (Port 8096)                       │
│                                                                   │
│  ┌───────────────┐  ┌─────────────┐  ┌──────────────┐          │
│  │  Auth & ACL   │  │   Adapter   │  │  Retrieval   │          │
│  │  (Identity &  │  │   (Protocol │  │   Engine     │          │
│  │  Permissions) │  │   Adapter)  │  │  (Semantic   │          │
│  └───────────────┘  └─────────────┘  │   Search)    │          │
│                                       └──────────────┘          │
│  ┌──────────────────────────────────────────────────┐          │
│  │     Codegraph Engine (Tree-sitter Parser)        │          │
│  │  Languages: Go │ Python │ TypeScript │ JavaScript│          │
│  │  Features: Symbol extraction, Call graph,        │          │
│  │            Impact analysis, Semantic indexing    │          │
│  └──────────────────────────────────────────────────┘          │
└─────────────────┬────────────────────────────────────────────────┘
                  │
┌─────────────────▼────────────────────────────────────────────────┐
│                   Storage & Scheduling Layer                      │
│                                                                   │
│  ┌──────────────┐   ┌──────────────┐   ┌────────────────┐      │
│  │   SQLite     │   │    Worker    │   │   Watchdog     │      │
│  │  (Global DB) │   │  (Task Queue)│   │ (Health Check) │      │
│  │  - Metadata  │   │  - Indexing  │   │  - Monitoring  │      │
│  │  - Sessions  │   │  - Cleanup   │   │  - Auto-heal   │      │
│  └──────────────┘   └──────────────┘   └────────────────┘      │
│                                                                   │
│  ┌───────────────────────────────────────────────────────┐      │
│  │   Teams DB (Multi-tenancy + Secrets Management)       │      │
│  │   - Isolated team workspaces                          │      │
│  │   - Encrypted credential storage                      │      │
│  └───────────────────────────────────────────────────────┘      │
└──────────────────────────────────────────────────────────────────┘
```

### 📦 Core Modules

| Module | Responsibility | Tech Stack |
|--------|---------------|------------|
| **Gateway** | HTTP API Gateway + MCP Server | Gin + modernc.org/sqlite |
| **Worker** | Async task processing (incremental indexing, scheduled cleanup) | Goroutine Pool |
| **Codegraph** | Code semantic parsing & impact analysis | Tree-sitter (multi-language) |
| **Retrieval** | Multi-modal retrieval (conversation/documents/code) | Custom vectorization + BM25 hybrid |
| **Auth/ACL** | Authentication + fine-grained access control | UUID + Role-based ACL |
| **Obsidian Adapter** | Markdown note indexing & wikilink parsing | File watching + incremental sync |

---

## 🚀 Quick Start

### Prerequisites

- **Go 1.23+** (required)
- **Node.js 18+** (optional, only needed for MCP client development)
- **Operating System**: Windows 10/11, Linux, or macOS

### Installation

#### Windows

```bash
# 1. Clone the repository
git clone https://github.com/j499712089/Memory-for-AI.git
cd Memory-for-AI/gateway

# 2. Set environment variables (optional)
set MEMORY_PLUS_DIR=F:\memory_plus
set GATEWAY_PORT=8096

# 3. Build and run
go build -o gateway.exe cmd/gateway/main.go
gateway.exe

# 4. Health check
curl http://127.0.0.1:8096/health
```

#### macOS / Linux

```bash
# 1. Clone the repository
git clone https://github.com/j499712089/Memory-for-AI.git
cd Memory-for-AI/gateway

# 2. Set environment variables (optional)
export MEMORY_PLUS_DIR="$HOME/memory_plus"
export GATEWAY_PORT=8096

# 3. Build and run
make build
./gateway

# 4. Health check
curl http://127.0.0.1:8096/health
```

### Docker Deployment

```bash
docker run -d \
  -p 8096:8096 \
  -v /path/to/data:/data \
  -e GATEWAY_PORT=8096 \
  --name memory-gateway \
  j499712089/memory-gateway:latest
```

---

## 🔌 Client Integration

### Claude Desktop (macOS / Windows)

1. **Install Claude Desktop**
   - Download from [claude.ai/download](https://claude.ai/download)

2. **Configure MCP Server**
   
   **macOS**: Edit `~/Library/Application Support/Claude/claude_desktop_config.json`
   
   **Windows**: Edit `%APPDATA%\Claude\claude_desktop_config.json`

   ```json
   {
     "mcpServers": {
       "memory-gateway": {
         "command": "node",
         "args": ["/path/to/Memory-for-AI/mcp-server/dist/index.js"],
         "env": {
           "GATEWAY_URL": "http://127.0.0.1:8096"
         }
       }
     }
   }
   ```

3. **Restart Claude Desktop**

### Codebuddy (Windows / Linux / macOS)

1. **Install Codebuddy**
   ```bash
   npm install -g @codebuddy/cli
   ```

2. **Configure Gateway**
   ```bash
   codebuddy config set memory.gateway http://127.0.0.1:8096
   codebuddy config set memory.enabled true
   ```

3. **Verify Connection**
   ```bash
   codebuddy memory status
   ```

### Multica Platform

1. **Access Multica Dashboard**
   - Navigate to Settings → Integrations

2. **Add Memory Gateway**
   - Gateway URL: `http://127.0.0.1:8096`
   - API Key: (generated from gateway admin panel)

3. **Enable for Agents**
   - Select agents that need memory access
   - Configure permission scopes (team/project/session)

### Custom Integration (HTTP API)

```bash
# Store memory
curl -X POST http://127.0.0.1:8096/api/v1/memories \
  -H "Content-Type: application/json" \
  -d '{
    "content": "User prefers dark mode",
    "type": "preference",
    "tags": ["ui", "settings"]
  }'

# Retrieve memory
curl -X GET "http://127.0.0.1:8096/api/v1/memories/search?q=dark+mode"

# Code impact analysis
curl -X POST http://127.0.0.1:8096/api/v1/codegraph/impact \
  -H "Content-Type: application/json" \
  -d '{
    "file": "auth.go",
    "function": "ValidateToken"
  }'
```

---

## 💡 Use Cases

### 1️⃣ Multi-Agent Collaboration Memory Sharing

```
Agent A: "What was the authentication approach we discussed last time?"
Gateway: [Retrieves team memory] → "OAuth 2.0 + JWT, documented in PRD #42"

Agent B: "Which modules will be affected if auth.go is modified?"
Gateway: [Codegraph analysis] → "3 files affected: handler.go, middleware.go, tests"
```

### 2️⃣ Project Knowledge Base Persistence

- **Session History Archiving** - All conversations automatically stored with semantic search support
- **Document Version Tracking** - Unified management of PRDs, design docs, and code comments
- **Decision Record Precipitation** - Automatic extraction of ADRs (Architecture Decision Records)

### 3️⃣ Obsidian Notes + AI Integration

```markdown
# Example Obsidian Note
The decision to use [[Microservice Architecture]] was made in [[Project Kickoff Meeting]]
→ Gateway automatically parses wikilinks to build knowledge graph
→ Agent can query: "All meeting records related to microservice architecture"
```

---

## 📊 Performance Metrics

| Metric | Value | Description |
|--------|-------|-------------|
| **Lines of Code** | 16,970+ lines Go | Excluding tests and vendor |
| **Test Coverage** | 85%+ | 50+ unit tests + integration tests |
| **Retrieval Latency** | <50ms (P95) | At 100K memory scale |
| **Concurrency** | 500 QPS | Single server (4 cores, 8GB RAM) |
| **Database** | SQLite (WAL mode) | Supports millions of records |

---

## 🛠️ Development Guide

### Project Structure

```
gateway/
├── cmd/
│   ├── gateway/        # Main service entry
│   └── worker/         # Background task entry
├── internal/
│   ├── adapter/        # LLM protocol adapters (OpenAI/Anthropic)
│   ├── auth/           # Authentication & authorization
│   ├── codegraph/      # Code graph engine
│   ├── db/             # Database layer
│   ├── retrieval/      # Retrieval engine
│   ├── skill/          # Multica skill integration
│   └── httpx/          # HTTP routing & middleware
├── mcp-server/         # MCP protocol server (TypeScript)
├── schema/             # Database schemas
├── test/               # Integration tests
└── Makefile            # Build scripts
```

### Local Development

```bash
# Run all tests
make test

# Code formatting
go fmt ./...

# Static analysis
go vet ./...

# Start dev server (with hot reload)
go run cmd/gateway/main.go
```

---

## 🤝 Contributing

We welcome all forms of contributions! Including but not limited to:

- 🐛 Submit bug reports
- ✨ Propose new features
- 📝 Improve documentation
- 🔧 Submit code patches

### Contribution Steps

1. Fork this repository
2. Create a feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Submit a Pull Request

---

## 📄 License

This project is licensed under the [MIT License](LICENSE).

---

## 🌟 Star History

If this project helps you, please give us a ⭐️ Star!

Your support motivates us to keep improving 💪

---

## 📞 Contact

- **Issue Tracker**: [GitHub Issues](https://github.com/j499712089/Memory-for-AI/issues)
- **Email**: 861892722@qq.com
- **Documentation**: [Full Docs](https://github.com/j499712089/Memory-for-AI/wiki)

---

<div align="center">

**Built with ❤️ by Memory-for-AI Team**

Empowering every AI Agent with long-term memory 🧠

</div>

---
---

# 简体中文

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
┌──────────────────────────────────────────────────────────────────┐
│                        AI Agent 客户端层                          │
│   Claude Desktop  │  Codebuddy  │  Multica  │  自定义 Agent     │
└─────────────────┬────────────────────────────────────────────────┘
                  │ MCP 协议 / HTTP API
┌─────────────────▼────────────────────────────────────────────────┐
│                  Memory Gateway (端口 8096)                       │
│                                                                   │
│  ┌───────────────┐  ┌─────────────┐  ┌──────────────┐          │
│  │  Auth & ACL   │  │   Adapter   │  │  Retrieval   │          │
│  │  (身份认证与  │  │   (协议适配)│  │   Engine     │          │
│  │   权限控制)   │  │             │  │  (语义检索)  │          │
│  └───────────────┘  └─────────────┘  └──────────────┘          │
│                                                                   │
│  ┌──────────────────────────────────────────────────┐          │
│  │     Codegraph 引擎 (Tree-sitter 解析器)          │          │
│  │  支持语言: Go │ Python │ TypeScript │ JavaScript │          │
│  │  功能: 符号提取, 调用图谱, 影响分析, 语义索引   │          │
│  └──────────────────────────────────────────────────┘          │
└─────────────────┬────────────────────────────────────────────────┘
                  │
┌─────────────────▼────────────────────────────────────────────────┐
│                      存储与调度层                                 │
│                                                                   │
│  ┌──────────────┐   ┌──────────────┐   ┌────────────────┐      │
│  │   SQLite     │   │    Worker    │   │   Watchdog     │      │
│  │  (全局数据库)│   │  (任务队列)  │   │  (健康检查)    │      │
│  │  - 元数据    │   │  - 增量索引  │   │  - 监控告警    │      │
│  │  - 会话记录  │   │  - 定时清理  │   │  - 自动修复    │      │
│  └──────────────┘   └──────────────┘   └────────────────┘      │
│                                                                   │
│  ┌───────────────────────────────────────────────────────┐      │
│  │   Teams DB (多租户隔离 + Secrets 管理)                │      │
│  │   - 团队工作空间隔离                                  │      │
│  │   - 加密凭据存储                                      │      │
│  └───────────────────────────────────────────────────────┘      │
└──────────────────────────────────────────────────────────────────┘
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
- **操作系统**: Windows 10/11、Linux 或 macOS

### 安装步骤

#### Windows

```bash
# 1. 克隆仓库
git clone https://github.com/j499712089/Memory-for-AI.git
cd Memory-for-AI/gateway

# 2. 配置环境变量（可选）
set MEMORY_PLUS_DIR=F:\memory_plus
set GATEWAY_PORT=8096

# 3. 编译并运行
go build -o gateway.exe cmd/gateway/main.go
gateway.exe

# 4. 健康检查
curl http://127.0.0.1:8096/health
```

#### macOS / Linux

```bash
# 1. 克隆仓库
git clone https://github.com/j499712089/Memory-for-AI.git
cd Memory-for-AI/gateway

# 2. 配置环境变量（可选）
export MEMORY_PLUS_DIR="$HOME/memory_plus"
export GATEWAY_PORT=8096

# 3. 编译并运行
make build
./gateway

# 4. 健康检查
curl http://127.0.0.1:8096/health
```

### Docker 快速部署

```bash
docker run -d \
  -p 8096:8096 \
  -v /path/to/data:/data \
  -e GATEWAY_PORT=8096 \
  --name memory-gateway \
  j499712089/memory-gateway:latest
```

---

## 🔌 客户端集成

### Claude Desktop (macOS / Windows)

1. **安装 Claude Desktop**
   - 从 [claude.ai/download](https://claude.ai/download) 下载

2. **配置 MCP 服务器**
   
   **macOS**: 编辑 `~/Library/Application Support/Claude/claude_desktop_config.json`
   
   **Windows**: 编辑 `%APPDATA%\Claude\claude_desktop_config.json`

   ```json
   {
     "mcpServers": {
       "memory-gateway": {
         "command": "node",
         "args": ["/path/to/Memory-for-AI/mcp-server/dist/index.js"],
         "env": {
           "GATEWAY_URL": "http://127.0.0.1:8096"
         }
       }
     }
   }
   ```

3. **重启 Claude Desktop**

### Codebuddy (Windows / Linux / macOS)

1. **安装 Codebuddy**
   ```bash
   npm install -g @codebuddy/cli
   ```

2. **配置 Gateway**
   ```bash
   codebuddy config set memory.gateway http://127.0.0.1:8096
   codebuddy config set memory.enabled true
   ```

3. **验证连接**
   ```bash
   codebuddy memory status
   ```

### Multica 平台

1. **访问 Multica 控制台**
   - 导航至 设置 → 集成

2. **添加 Memory Gateway**
   - Gateway URL: `http://127.0.0.1:8096`
   - API Key: (从 gateway 管理面板生成)

3. **为 Agent 启用**
   - 选择需要记忆访问的 Agent
   - 配置权限范围（团队/项目/会话）

### 自定义集成 (HTTP API)

```bash
# 存储记忆
curl -X POST http://127.0.0.1:8096/api/v1/memories \
  -H "Content-Type: application/json" \
  -d '{
    "content": "用户偏好深色模式",
    "type": "preference",
    "tags": ["ui", "settings"]
  }'

# 检索记忆
curl -X GET "http://127.0.0.1:8096/api/v1/memories/search?q=深色模式"

# 代码影响分析
curl -X POST http://127.0.0.1:8096/api/v1/codegraph/impact \
  -H "Content-Type: application/json" \
  -d '{
    "file": "auth.go",
    "function": "ValidateToken"
  }'
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
| **数据库性能** | SQLite (WAL 模式) | 支持百万级记录 |

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

Empowering every AI Agent with long-term memory 🧠

</div>
