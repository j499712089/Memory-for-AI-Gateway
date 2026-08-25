# 部署运维指南

## 概述

Memory Gateway 支持 Windows 本地部署（主要）和跨平台部署（实验性）。本文档涵盖：
- 环境准备
- 编译构建
- 配置管理
- 启动方式
- 监控日志
- 故障排查
- 备份恢复

## 系统要求

### 硬件要求

**最低配置**：
- CPU：2 核心
- 内存：4 GB
- 磁盘：20 GB 可用空间
- 网络：稳定互联网连接（调用上游 LLM API）

**推荐配置**：
- CPU：4 核心
- 内存：8 GB
- 磁盘：100 GB SSD
- 网络：带宽 ≥ 10 Mbps

### 软件要求

**必需**：
- Windows 10/11 或 Windows Server 2019+ (主要支持)
- Go 1.23+ (编译时)
- Node.js 20+ (MCP Server 和 Web Panel)
- Git (代码管理)

**可选**：
- NSSM (Windows Service 注册)
- Caddy/Nginx (生产环境 Web 托管)
- PowerShell 7+ (脚本执行)

## 目录结构

```
F:\memory_plus\
├── .runtime\                        # 运行时数据
│   ├── memory-gateway.db            # 全局库
│   ├── memory-gateway.db-wal        # WAL 文件
│   ├── memory-gateway.db-shm        # 共享内存
│   ├── secrets\                     # DPAPI 加密密钥
│   │   ├── key_ref_1.bin
│   │   └── key_ref_2.bin
│   ├── buffers\                     # 降级缓冲文件
│   │   └── 2026-08-19\
│   │       └── buffer_abc123.jsonl
│   └── gateway.log                  # 服务日志
├── 90_运行数据\
│   └── teams\
│       └── {team_id}\
│           └── memory.db            # 团队库
├── L0_每一轮记录\                   # L0 资产
│   └── 2026-08-19\
│       └── req_abc123.jsonl
├── L1_长久记忆\                     # L1 资产
├── L2_全域知识\                     # L2 资产
├── L3_团队身份\                     # L3 资产
└── gateway\
    ├── cmd\
    │   ├── gateway\main.go          # 主服务入口
    │   └── worker\main.go           # Worker 入口
    ├── config.yaml                  # 配置文件
    ├── schema\schema.sql            # 数据库 Schema
    ├── mcp-server\                  # MCP 服务
    │   ├── dist\                    # 编译产物
    │   ├── src\
    │   └── package.json
    └── web-panel\                   # Web 管理面板
        ├── dist\                    # 编译产物
        ├── src\
        └── package.json
```

## 编译构建

### 后端编译 (Go)

```bash
cd F:\memory_plus\gateway

# 编译主服务
go build -o gateway.exe cmd/gateway/main.go

# 编译 Worker (可选，主服务已内置)
go build -o worker.exe cmd/worker/main.go

# 交叉编译 (Linux)
GOOS=linux GOARCH=amd64 go build -o gateway cmd/gateway/main.go
```

**编译优化**：
```bash
# 生产版本 (减小体积、禁用调试符号)
go build -ldflags="-s -w" -o gateway.exe cmd/gateway/main.go
```

### MCP Server 编译 (Node.js)

```bash
cd F:\memory_plus\gateway\mcp-server

# 安装依赖
npm install

# 编译 TypeScript → JavaScript
npm run build

# 测试运行
npm run start:stdio
```

**产物**：`mcp-server/dist/index.js`

### Web Panel 编译 (Vue 3)

```bash
cd F:\memory_plus\gateway\web-panel

# 安装依赖
npm install

# 开发模式
npm run dev

# 生产编译
npm run build
```

**产物**：`web-panel/dist/` 静态文件

## 配置管理

### 配置文件 (config.yaml)

```yaml
# 服务器配置
server:
  host: "0.0.0.0"
  port: 8096
  read_timeout: 30s
  write_timeout: 120s
  max_header_bytes: 1048576  # 1MB

# 数据库配置
database:
  global_db_path: "F:\\memory_plus\\.runtime\\memory-gateway.db"
  teams_root: "F:\\memory_plus\\90_运行数据\\teams"

# 密钥管理
secrets:
  secrets_dir: "F:\\memory_plus\\.runtime\\secrets"
  use_stub: false  # true=明文文件(开发), false=DPAPI(生产)

# 模型配置
models:
  models_json_path: "F:\\memory_plus\\gateway\\models.json"

# 日志配置
logging:
  level: "info"  # debug/info/warn/error
  output: "F:\\memory_plus\\.runtime\\gateway.log"
  max_size: 100  # MB
  max_backups: 10
  max_age: 30  # 天
  compress: true

# Worker 配置
worker:
  enabled: true
  outbox_interval: 10s
  buffer_interval: 30s
  task_interval: 5s
```

### 环境变量

```bash
# 覆盖配置文件
export GATEWAY_CONFIG_PATH="F:\memory_plus\gateway\config.yaml"

# 覆盖端口
export GATEWAY_PORT=8096

# 日志级别
export GATEWAY_LOG_LEVEL=debug
```

### 密钥配置 (models.json)

```json
{
  "models": [
    {
      "id": "deepseek-chat",
      "provider": "deepseek",
      "protocol": "chat_completions",
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "sk-xxx",  // 首次导入后会加密
      "capabilities": {
        "supportsToolCall": true,
        "maxInputTokens": 128000
      }
    }
  ]
}
```

**安全注意**：
- 首次启动会自动将 `api_key` 加密到 `secrets_dir`
- 数据库只存 `api_key_ref`，不存明文
- 定期轮换密钥

## 启动方式

### 开发环境

**Terminal 1 - Gateway**：
```bash
cd F:\memory_plus\gateway
go run cmd/gateway/main.go
```

**Terminal 2 - MCP Server**：
```bash
cd F:\memory_plus\gateway\mcp-server
npm run dev:stdio
```

**Terminal 3 - Web Panel**：
```bash
cd F:\memory_plus\gateway\web-panel
npm run dev
```

访问：
- Gateway API: http://localhost:8096
- Web Panel: http://localhost:5173

### 生产环境

#### 方式 1：直接运行

```bash
# 启动 Gateway
cd F:\memory_plus\gateway
.\gateway.exe

# 启动 MCP Server (另一终端)
cd F:\memory_plus\gateway\mcp-server
node dist/index.js --transport http
```

#### 方式 2：Windows Service (推荐)

使用 NSSM 注册为系统服务：

```powershell
# 下载 NSSM: https://nssm.cc/download
nssm install MemoryGateway "F:\memory_plus\gateway\gateway.exe"

# 配置工作目录
nssm set MemoryGateway AppDirectory "F:\memory_plus\gateway"

# 配置日志
nssm set MemoryGateway AppStdout "F:\memory_plus\.runtime\gateway_stdout.log"
nssm set MemoryGateway AppStderr "F:\memory_plus\.runtime\gateway_stderr.log"

# 启动服务
nssm start MemoryGateway

# 查看状态
nssm status MemoryGateway

# 停止服务
nssm stop MemoryGateway

# 卸载服务
nssm remove MemoryGateway confirm
```

**MCP Server 服务化**：
```powershell
nssm install MemoryGatewayMCP "C:\Program Files\nodejs\node.exe" "F:\memory_plus\gateway\mcp-server\dist\index.js" "--transport" "http"
nssm set MemoryGatewayMCP AppDirectory "F:\memory_plus\gateway\mcp-server"
nssm start MemoryGatewayMCP
```

#### 方式 3：Docker (实验性)

```dockerfile
# Dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o gateway cmd/gateway/main.go

FROM node:20-alpine AS mcp-builder
WORKDIR /app
COPY mcp-server/package*.json ./
RUN npm ci
COPY mcp-server/ ./
RUN npm run build

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/gateway .
COPY --from=mcp-builder /app/dist ./mcp-server/dist
COPY config.yaml .
EXPOSE 8096 8097
CMD ["./gateway"]
```

构建运行：
```bash
docker build -t memory-gateway:latest .
docker run -d -p 8096:8096 -p 8097:8097 \
  -v F:/memory_plus:/memory_plus \
  memory-gateway:latest
```

## 监控日志

### 日志级别

- **DEBUG**：详细调试信息（开发环境）
- **INFO**：正常运行信息（生产默认）
- **WARN**：警告信息（非致命错误）
- **ERROR**：错误信息（需关注）

### 日志格式

```
2026-08-19T10:00:00.123Z [INFO] gateway/main.go:45 Starting Memory Gateway...
2026-08-19T10:00:01.456Z [INFO] db/db.go:67 Database opened: memory-gateway.db
2026-08-19T10:00:02.789Z [INFO] httpx/router.go:82 Server listening on :8096
```

### 日志查看

**实时查看**：
```bash
tail -f F:\memory_plus\.runtime\gateway.log
```

**搜索错误**：
```bash
grep ERROR F:\memory_plus\.runtime\gateway.log
```

**按时间过滤**：
```bash
grep "2026-08-19T10:" F:\memory_plus\.runtime\gateway.log
```

### 日志轮转

配置文件已启用日志轮转：
- 单文件最大 100MB
- 保留最近 10 个备份
- 保存 30 天
- 自动压缩

### 关键日志示例

**正常启动**：
```
[INFO] Starting Memory Gateway...
[INFO] Database opened: memory-gateway.db
[INFO] Database journal mode: wal
[INFO] Secrets manager initialized
[INFO] Loaded 5 models from models.json
[INFO] Recovery completed: outbox=0 buffer=0 tasks=0
[INFO] Server listening on :8096
```

**录入失败（触发 outbox）**：
```
[WARN] Failed to write L0 file: disk full
[INFO] Wrote to outbox: event_id=outbox_abc123
```

**幂等性命中**：
```
[INFO] Idempotency cache hit: key=550e8400-e29b-41d4-a716-446655440000
```

## 健康检查

### 健康端点

```bash
curl http://localhost:8096/health
```

**正常响应**：
```json
{
  "status": "healthy",
  "journal_mode": "wal",
  "version": "1.0.0"
}
```

**异常响应** (503)：
```json
{
  "status": "unhealthy",
  "error": "database connection failed",
  "message": "unable to open database file"
}
```

### Prometheus 指标 (未来)

```bash
curl http://localhost:8096/metrics
```

**关键指标**：
- `gateway_requests_total{status="200",channel="default"}`
- `gateway_request_duration_seconds{quantile="0.99"}`
- `gateway_recording_failures_total`
- `gateway_outbox_queue_size`

## 故障排查

### 常见问题

#### 1. 数据库锁定 (SQLITE_BUSY)

**现象**：
```
[ERROR] database is locked
```

**原因**：
- WAL checkpoint 阻塞
- 长时间未提交事务
- 并发写入超限

**解决**：
```sql
-- 检查 WAL 大小
PRAGMA wal_checkpoint(FULL);

-- 增加 busy_timeout
PRAGMA busy_timeout = 10000;
```

#### 2. 幂等性缓存过期

**现象**：
重复请求返回不同响应。

**原因**：
`idempotency_cache` 记录已过期（24 小时）。

**解决**：
```sql
-- 检查缓存
SELECT * FROM idempotency_cache WHERE key = ?;

-- 清理过期记录
DELETE FROM idempotency_cache WHERE expires_at < datetime('now');
```

#### 3. Outbox 队列堆积

**现象**：
```
[WARN] Outbox queue size: 150
```

**原因**：
- L0 文件写入持续失败
- Worker 未启动或崩溃

**解决**：
```sql
-- 查看队列
SELECT * FROM outbox WHERE status = 'pending' ORDER BY created_at;

-- 手动重试
UPDATE outbox SET status = 'pending', retry_count = 0 WHERE id = ?;
```

#### 4. 密钥解密失败

**现象**：
```
[ERROR] Failed to decrypt API key: invalid entropy
```

**原因**：
- DPAPI 密钥被系统重置
- `secrets_dir` 文件损坏

**解决**：
1. 重新导入 `models.json`
2. 或切换到 Stub 模式（开发环境）：
   ```yaml
   secrets:
     use_stub: true
   ```

#### 5. MCP Server 连接失败

**现象**：
Claude Desktop 提示 "MCP server not responding"。

**原因**：
- MCP Server 未启动
- stdio 管道阻塞

**解决**：
```bash
# 检查进程
ps aux | grep "node.*mcp-server"

# 重启 MCP Server
pkill -f mcp-server
npm run start:stdio
```

### 日志分析

**查找高频错误**：
```bash
grep ERROR gateway.log | cut -d' ' -f4- | sort | uniq -c | sort -rn | head -10
```

**统计请求状态码**：
```bash
grep "HTTP" gateway.log | awk '{print $NF}' | sort | uniq -c
```

**分析慢请求**：
```bash
grep "duration" gateway.log | awk '$NF > 1000' | head -20
```

## 备份恢复

### 数据库备份

**热备份** (服务运行中)：
```bash
# 备份全局库
copy F:\memory_plus\.runtime\memory-gateway.db F:\backups\memory-gateway.db.$(date +%Y%m%d)
copy F:\memory_plus\.runtime\memory-gateway.db-wal F:\backups\memory-gateway.db-wal.$(date +%Y%m%d)

# 备份团队库
copy F:\memory_plus\90_运行数据\teams\{team_id}\memory.db F:\backups\team_{team_id}.db.$(date +%Y%m%d)
```

**冷备份** (停止服务)：
```bash
# 停止服务
nssm stop MemoryGateway

# 使用 SQLite 内置备份
sqlite3 F:\memory_plus\.runtime\memory-gateway.db ".backup F:\backups\memory-gateway.db.$(date +%Y%m%d)"

# 重启服务
nssm start MemoryGateway
```

### 资产备份

**L0/L1/L2/L3 资产**：
```bash
# 压缩备份
tar -czf F:\backups\assets_$(date +%Y%m%d).tar.gz \
  F:\memory_plus\L0_每一轮记录 \
  F:\memory_plus\L1_长久记忆 \
  F:\memory_plus\L2_全域知识 \
  F:\memory_plus\L3_团队身份
```

### 密钥备份

```bash
# 备份 DPAPI 加密文件
copy F:\memory_plus\.runtime\secrets F:\backups\secrets_$(date +%Y%m%d) /E
```

**注意**：
- DPAPI 密钥绑定到 Windows 用户账户
- 恢复时需在同一账户下操作

### 恢复流程

1. **停止服务**：
   ```bash
   nssm stop MemoryGateway
   nssm stop MemoryGatewayMCP
   ```

2. **恢复数据库**：
   ```bash
   copy F:\backups\memory-gateway.db.20260819 F:\memory_plus\.runtime\memory-gateway.db
   ```

3. **恢复资产**：
   ```bash
   tar -xzf F:\backups\assets_20260819.tar.gz -C F:\memory_plus\
   ```

4. **恢复密钥**：
   ```bash
   copy F:\backups\secrets_20260819 F:\memory_plus\.runtime\secrets /E
   ```

5. **重启服务**：
   ```bash
   nssm start MemoryGateway
   nssm start MemoryGatewayMCP
   ```

6. **验证健康**：
   ```bash
   curl http://localhost:8096/health
   ```

### 自动备份脚本

**PowerShell 脚本** (backup.ps1)：
```powershell
# 备份脚本
$BackupRoot = "F:\backups"
$Date = Get-Date -Format "yyyyMMdd_HHmmss"
$BackupDir = "$BackupRoot\$Date"

# 创建备份目录
New-Item -ItemType Directory -Path $BackupDir

# 备份数据库
Copy-Item "F:\memory_plus\.runtime\memory-gateway.db*" $BackupDir

# 备份资产 (仅最近 7 天)
$Assets = @("L0_每一轮记录", "L1_长久记忆", "L2_全域知识", "L3_团队身份")
foreach ($Asset in $Assets) {
    Get-ChildItem "F:\memory_plus\$Asset" -Recurse | 
      Where-Object { $_.LastWriteTime -gt (Get-Date).AddDays(-7) } | 
      Copy-Item -Destination "$BackupDir\$Asset" -Recurse
}

# 备份密钥
Copy-Item "F:\memory_plus\.runtime\secrets" "$BackupDir\secrets" -Recurse

# 压缩
Compress-Archive -Path $BackupDir -DestinationPath "$BackupDir.zip"
Remove-Item $BackupDir -Recurse

# 清理 30 天前的备份
Get-ChildItem $BackupRoot -Filter "*.zip" | 
  Where-Object { $_.LastWriteTime -lt (Get-Date).AddDays(-30) } | 
  Remove-Item
```

**定时任务** (Windows Task Scheduler)：
```powershell
# 注册每日凌晨 3 点备份
$Action = New-ScheduledTaskAction -Execute "PowerShell.exe" -Argument "-File F:\memory_plus\gateway\backup.ps1"
$Trigger = New-ScheduledTaskTrigger -Daily -At 3:00AM
Register-ScheduledTask -TaskName "MemoryGatewayBackup" -Action $Action -Trigger $Trigger
```

## 性能优化

### 数据库优化

**定期 VACUUM**：
```sql
-- 压缩数据库，回收空间
VACUUM;

-- 分析统计信息，优化查询计划
ANALYZE;
```

**索引优化**：
```sql
-- 查看索引使用情况
SELECT * FROM sqlite_stat1;

-- 创建覆盖索引
CREATE INDEX IF NOT EXISTS idx_turn_ledger_conv_status 
  ON turn_ledger(conversation_id, final_status);
```

### 连接池调优

```go
db.SetMaxOpenConns(25)      // 根据 CPU 核心数调整
db.SetMaxIdleConns(5)       // 保持少量空闲连接
db.SetConnMaxLifetime(5*time.Minute)
```

### 日志降级

生产环境使用 INFO 级别：
```yaml
logging:
  level: "info"  # 避免 debug 产生大量日志
```

### 缓存策略

**Redis 缓存层** (未来扩展)：
- API Key 校验结果缓存（TTL 5 分钟）
- 幂等性缓存迁移到 Redis
- Session 信息缓存

## 升级流程

### 版本升级

1. **备份当前版本**：
   ```bash
   copy gateway.exe gateway.exe.backup
   ```

2. **停止服务**：
   ```bash
   nssm stop MemoryGateway
   ```

3. **替换可执行文件**：
   ```bash
   copy gateway_v2.exe gateway.exe
   ```

4. **运行迁移脚本** (如有)：
   ```bash
   .\gateway.exe migrate
   ```

5. **重启服务**：
   ```bash
   nssm start MemoryGateway
   ```

6. **验证健康**：
   ```bash
   curl http://localhost:8096/health
   ```

### 回滚流程

```bash
nssm stop MemoryGateway
copy gateway.exe.backup gateway.exe
nssm start MemoryGateway
```

## 安全加固

### 防火墙配置

```powershell
# 允许 Gateway 端口
New-NetFirewallRule -DisplayName "Memory Gateway" -Direction Inbound -LocalPort 8096 -Protocol TCP -Action Allow

# 仅本地访问 MCP Server
New-NetFirewallRule -DisplayName "Memory Gateway MCP" -Direction Inbound -LocalPort 8097 -Protocol TCP -Action Allow -RemoteAddress 127.0.0.1
```

### HTTPS 配置

使用 Caddy 反向代理：

```caddyfile
# Caddyfile
gateway.example.com {
    reverse_proxy localhost:8096
    
    tls {
        protocols tls1.3
    }
    
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
    }
}
```

### 密钥轮换

```bash
# 1. 生成新 API Key
curl -X POST http://localhost:8096/api/api-keys \
  -H "Authorization: Bearer admin_key" \
  -d '{"team_id":"team_abc","scopes":["gateway","mcp"]}'

# 2. 更新客户端配置

# 3. 禁用旧 Key（当前方式）
curl -X PUT http://localhost:8096/api/api-keys/old_key_id \
  -H "Authorization: Bearer admin_key" \
  -d '{"enabled":false}'
```

**注意**：`DELETE /api/api-keys/:id` 端点尚未实现（Phase 2 规划），当前通过 `PUT` 更新 `enabled: false` 来禁用密钥。

---

**文档版本**: 1.0  
**创建日期**: 2026-08-19  
**维护者**: 首席架构师 高见远
