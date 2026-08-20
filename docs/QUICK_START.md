# Memory Gateway 快速开始

本指南面向 Windows 发布包用户。完成下载、配置和首次调用通常不超过 5 分钟；首次打开管理面板目标为 15 秒内。

## 1. 下载、解压并启动

1. 从 GitHub Releases 下载完整压缩包，不要只下载 `gateway.exe`。
2. 解压到没有中文和空格的目录，例如 `F:\memory_plus\gateway`。
3. 确认根目录包含 `gateway.exe`、`.env.example`、`start-memory-gateway.bat`、`stop-memory-gateway.bat`、`check-status.bat` 和 `web-panel`。
4. 双击 `start-memory-gateway.bat`。

脚本会自动复制 `.env.example` 为 `.env`（若不存在）、创建 `.runtime/secrets`、启动 Gateway 和 Web 面板，并打开 `http://127.0.0.1:5173/quick-start`。后端健康检查地址为 `http://127.0.0.1:8096/health`。

## 2. Web 面板六步配置

### 步骤 1：创建 Team

填写 Team 名称、唯一 slug 和可选描述。Team 是 API Key、身份卡片、上游通道和数据的隔离边界。

### 步骤 2：生成下游 API Key

为 Team 创建调用 Key，选择所需权限。明文只显示一次，请立即保存。这个 Key 通常以 `gw_` 开头，应用调用 Gateway 时放在 `Authorization: Bearer <key>` 中。

### 步骤 3：配置上游通道

选择 Anthropic、OpenAI 或 Codex，粘贴从对应供应商获取的上游 Key，点击测试连接并保存。上游 Key 只保存在 Gateway 的运行时 secrets 目录，不要提交到代码仓库或发给客户端。此步可跳过，之后在“上游通道”页面完成。

### 步骤 4：创建身份卡片

填写身份卡片名称、Persona 和可选 Agent ID，用于描述调用时的助手身份和行为约束。

### 步骤 5：验证配置

回到向导或管理面板，确认 Team、下游 API Key、上游通道和身份卡片均显示为已配置；使用“测试连接”确认 Gateway 能访问上游服务。

### 步骤 6：开始调用

使用下游 API Key 调用 Gateway。以下示例调用 Anthropic 兼容端点：

```bash
curl -X POST http://127.0.0.1:8096/v1/messages ^
  -H "Authorization: Bearer gw_your_key" ^
  -H "Content-Type: application/json" ^
  -d "{\"model\":\"claude-3-5-sonnet-20241022\",\"max_tokens\":128,\"messages\":[{\"role\":\"user\",\"content\":\"你好\"}]}"
```

## 3. 日常操作

- 停止服务：双击 `stop-memory-gateway.bat`。
- 检查服务：双击 `check-status.bat`。
- 管理上游通道：打开 `http://127.0.0.1:5173/upstream-keys`。
- 管理下游 API Key：打开 `http://127.0.0.1:5173/api-keys`。
- 管理身份卡片：打开 `http://127.0.0.1:5173/identity-cards`。

## 4. 数据、端口与备份

Gateway API 使用 8096 端口，Web 面板使用 5173 端口。运行数据默认位于 `${MEMORY_PLUS_DIR}/.runtime/`，团队数据位于 `${MEMORY_PLUS_DIR}/90_运行数据/teams/`。升级前先停止服务，并复制 `.runtime/memory-gateway.db` 到备份目录；至少保留最近 7 天的每日备份。

回滚时恢复上一版 `gateway.exe` 和对应数据库备份，随后重新运行 `start-memory-gateway.bat`。

## 5. 故障排查

- 浏览器打不开：运行 `check-status.bat`，确认 5173 正在监听；也可直接访问 `http://127.0.0.1:5173/quick-start`。
- 后端不健康：访问 `/health`，检查 8096 是否被占用、`.env` 的 `MEMORY_PLUS_DIR` 是否存在且可写。
- 前端启动失败：确认 Node.js 18+，进入 `web-panel` 运行 `npm install` 后重试。
- 调用返回认证错误：确认客户端使用的是 Gateway 生成的下游 Key，而不是供应商上游 Key。
- 调用返回上游错误：在“上游通道”页面重新测试 Key，并检查供应商额度和网络连接。

更多部署验证和手动启动命令见 [../DEPLOY.md](../DEPLOY.md)。
