# Memory Gateway 快速开始指南

本指南帮助您在 5 分钟内完成 Memory Gateway 的安装和首次调用。

## 前置要求

- Windows 10/11
- Node.js 16+（前端面板需要）
- 有效的 Anthropic/OpenAI API Key（上游通道）

## 第一步：下载和启动

1. 从 [GitHub Releases](https://github.com/j499712089/Memory-for-AI/releases) 下载最新版本
2. 解压到任意目录（例如 `F:\memory_plus\gateway`）
3. 双击 `start-memory-gateway.bat`

**启动脚本会自动完成：**
- 检查并创建 `.env` 文件
- 创建数据目录（`data/.runtime/secrets` 等）
- 检查前端依赖（首次运行会自动 `npm install`）
- 启动后端服务（端口 8096）
- 启动前端面板（端口 5173）
- 等待服务就绪后打开浏览器

**首次启动大约需要 10-15 秒**

## 第二步：完成向导配置

浏览器会自动打开 `http://localhost:5173/quick-start`，按照 6 步向导操作：

### 步骤 1：创建 Team

- **Team 名称**：例如「我的 AI 助手团队」
- **Slug**：例如 `my-team`（仅限小写字母、数字、连字符）
- **描述**：可选

### 步骤 2：生成 API Key

- **Key 名称**：例如「Production Key」
- **权限范围**：勾选 `memories:read` 和 `memories:write`

**重要：** 生成后立即复制保存 API Key（`gw_xxxxx`），它只展示一次。

### 步骤 3：创建身份卡片

- **身份卡片名称**：例如「我的 AI 助手」
- **Persona**：例如「你是一个专业的技术顾问...」
- **Agent ID**：可选，用于绑定客户端 Agent

### 步骤 4：测试连接

点击「测试连接」按钮，验证 API Key 是否正常工作。

### 步骤 5：配置上游通道

**这是新增步骤，无需手动创建文件！**

- 选择服务商：Anthropic / OpenAI / Codex
- 粘贴您的上游 API Key
- 点击「测试连接」验证
- 保存（自动存储到 `data/.runtime/secrets/<team_id>_<provider>.key`）

**可以跳过此步，稍后在管理面板配置。**

### 步骤 6：完成

显示配置摘要，点击「进入管理面板」。

## 第三步：首次 LLM 调用

使用生成的 API Key（`gw_xxxxx`）调用网关：

```bash
curl -X POST http://127.0.0.1:8096/v1/messages \
  -H "Authorization: Bearer gw_xxxxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "max_tokens": 1024,
    "messages": [
      {"role": "user", "content": "你好，介绍一下你自己"}
    ]
  }'
```

**成功返回：**
```json
{
  "id": "msg_xxxxx",
  "type": "message",
  "role": "assistant",
  "content": [...]
}
```

## 常见问题

### Q1: 启动后浏览器没有自动打开？

手动访问 `http://localhost:5173/quick-start`

### Q2: 前端启动失败？

检查 Node.js 版本（需要 16+）：
```bash
node --version
```

### Q3: 上游 Key 和下游 API Key 有什么区别？

- **上游 Key**：您从 Anthropic/OpenAI 获取的真实 API Key（Team 级别）
- **下游 API Key**：Gateway 生成的 Key（`gw_xxxxx`），用于客户端调用网关

### Q4: 如何停止服务？

双击 `stop-memory-gateway.bat`

### Q5: 如何检查服务状态？

双击 `check-status.bat`

## 高级配置

### 管理上游通道

访问 `http://localhost:5173/upstream-keys` 可以：
- 查看已配置的上游通道
- 添加多个服务商的 Key
- 测试连接状态
- 删除过期的 Key

### 管理 API Keys

访问 `http://localhost:5173/api-keys` 可以：
- 查看所有下游 API Key
- 生成新的 Key（可指定不同权限）
- 禁用/删除 Key

### 管理身份卡片

访问 `http://localhost:5173/identity-cards` 可以：
- 查看所有身份卡片
- 编辑 Persona 和职责
- 绑定/解绑 Agent ID

## 下一步

- 阅读 [API 文档](./API.md) 了解完整的 API 能力
- 阅读 [架构文档](./02_architecture_design.md) 了解系统设计
- 查看 [部署指南](./05_deployment_guide.md) 了解生产环境部署

## 故障排查

### 后端启动失败

1. 检查端口占用：`netstat -ano | findstr :8096`
2. 查看日志：`logs/gateway.log`
3. 确认 `.env` 文件存在

### 前端启动失败

1. 删除 `node_modules` 重新安装：`npm install`
2. 检查 Node.js 版本：`node --version`（需要 16+）
3. 查看控制台错误信息

### API 调用失败

1. 确认上游通道已配置（访问 `/upstream-keys`）
2. 检查 API Key 是否正确（不要混淆上游和下游 Key）
3. 查看后端日志：`logs/gateway.log`

## 技术支持

- GitHub Issues: https://github.com/j499712089/Memory-for-AI/issues
- 文档中心: https://github.com/j499712089/Memory-for-AI/tree/main/docs
