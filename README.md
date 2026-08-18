# Memory Gateway

Memory Gateway MVP - Phase 1 Foundation

## Quick Start

### Prerequisites

- Go 1.21 or higher
- Windows (for DPAPI support) or Linux/macOS (uses stub encryption)

### Build and Run

```bash
cd F:\memory_plus\gateway
go mod tidy
go build -o gateway.exe cmd/gateway/main.go
./gateway.exe
```

### Configuration

Configuration is done via environment variables:

- `MEMORY_PLUS_DIR` - Base directory (default: `F:\memory_plus`)
- `GATEWAY_PORT` - Server port (default: 8096)
- `MODELS_JSON_PATH` - Path to models.json (default: `F:\memory_plus\00_系统\models.json`)

### Health Check

```bash
curl http://127.0.0.1:8096/health
```

Expected response:
```json
{
  "status": "healthy",
  "journal_mode": "wal",
  "version": "1.0.0"
}
```

## API Endpoints

### Health
- `GET /health` - Health check (no auth required)

### Teams
- `GET /api/teams` - List teams
- `POST /api/teams` - Create team
- `GET /api/teams/{team_id}` - Get team details

### API Keys
- `GET /api/api-keys` - List API keys
- `POST /api/api-keys` - Create API key (returns plaintext once)

All `/api/*` endpoints require authentication via `Authorization: Bearer <key>` or `x-api-key: <key>` header.

## Testing

```bash
cd F:\memory_plus\gateway
go test ./...
```

## Database

- **Global DB**: `F:\memory_plus\.runtime\memory-gateway.db`
- **Team DBs**: `F:\memory_plus\90_运行数据\teams\{team_id}\memory.db`

Both use WAL mode for concurrent read/write operations.

## Secrets

API keys and upstream channel credentials are encrypted using:
- **Windows**: DPAPI (Data Protection API)
- **Linux/macOS**: AES-256 (stub implementation)

Encrypted secrets are stored in `F:\memory_plus\.runtime\secrets\`.

## Architecture

```
gateway/
├── cmd/gateway/          # Main application entry point
├── internal/
│   ├── config/          # Configuration management
│   ├── db/              # Database operations
│   ├── secrets/         # Secret encryption (DPAPI/stub)
│   ├── auth/            # API key authentication
│   ├── httpx/           # HTTP handlers and middleware
│   ├── idgen/           # ID generation utilities
│   ├── hashutil/        # Hashing utilities
│   └── paths/           # Path safety utilities
├── schema/              # Database schema
└── test/                # Tests
```
