package inject

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gateway/internal/adapter"
	"gateway/internal/idgen"
)

type Snapshot struct {
	RequestID   string
	TurnID      string
	Package     adapter.InjectionPackage
	Text        string
	TextPath    string
	TokenBudget int
}

// RecordSnapshot writes the compact injection audit row after the payload has
// been assembled and before it is forwarded upstream.
func RecordSnapshot(ctx context.Context, database *sql.DB, snapshot Snapshot) error {
	if database == nil {
		return fmt.Errorf("snapshot database is nil")
	}
	if snapshot.RequestID == "" || snapshot.TurnID == "" {
		return fmt.Errorf("request id and turn id are required")
	}
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire snapshot connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	var existing int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM injection_snapshots WHERE request_id=? AND turn_id=?`, snapshot.RequestID, snapshot.TurnID).Scan(&existing); err != nil {
		return fmt.Errorf("check injection snapshot: %w", err)
	}
	if existing > 0 {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return fmt.Errorf("commit existing injection snapshot: %w", err)
		}
		committed = true
		return nil
	}
	// A nil SourceEventIDs marshals to the JSON literal "null", which the E2E
	// harness reads back as None. Normalize to an empty array so downstream
	// readers always see "[]" for an empty source set.
	sourceIDs := snapshot.Package.SourceEventIDs
	if sourceIDs == nil {
		sourceIDs = []string{}
	}
	sources, err := json.Marshal(sourceIDs)
	if err != nil {
		return fmt.Errorf("marshal snapshot source ids: %w", err)
	}
	usedTokens := (len([]rune(snapshot.Text)) + 3) / 4
	_, err = conn.ExecContext(ctx, `INSERT INTO injection_snapshots
		(id, request_id, turn_id, manifest_version, source_ids_json, token_budget, used_tokens, truncated, injected_text_path, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		idgen.NewID(), snapshot.RequestID, snapshot.TurnID, snapshot.Package.ManifestVersion, string(sources),
		snapshot.TokenBudget, usedTokens, boolInt(snapshot.Package.Truncated), nullable(snapshot.TextPath), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert injection snapshot: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit injection snapshot: %w", err)
	}
	committed = true
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
