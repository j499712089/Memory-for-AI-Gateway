package codegraph

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gateway/internal/db"
)

// ImpactResult is the answer to "what is affected if this symbol changes":
// the symbol itself plus its direct callers and callees up to a depth.
type ImpactResult struct {
	Symbol  db.SymbolRef   `json:"symbol"`
	Callers []db.SymbolRef `json:"callers"`
	Callees []db.SymbolRef `json:"callees"`
}

// Impact resolves a symbol and returns both call directions. depth bounds
// the traversal (default 3). An empty symbol name still resolves callers of
// a file-wide search by name.
func Impact(ctx context.Context, database *sql.DB, repoID, filePath, symbolName string, depth int) (ImpactResult, error) {
	if database == nil {
		return ImpactResult{}, fmt.Errorf("codegraph database is nil")
	}
	if strings.TrimSpace(repoID) == "" {
		return ImpactResult{}, fmt.Errorf("repo id is required")
	}
	if strings.TrimSpace(symbolName) == "" {
		return ImpactResult{}, fmt.Errorf("symbol name is required")
	}
	if depth <= 0 {
		depth = 3
	}
	symbol, err := db.FindSymbolInRepo(ctx, database, repoID, filePath, symbolName)
	if err != nil {
		return ImpactResult{}, fmt.Errorf("find symbol %q in repo %s: %w", symbolName, repoID, err)
	}
	callers, err := db.ImpactNeighbors(ctx, database, symbol.ID, "callers", depth)
	if err != nil {
		return ImpactResult{}, fmt.Errorf("resolve callers of %q: %w", symbolName, err)
	}
	callees, err := db.ImpactNeighbors(ctx, database, symbol.ID, "callees", depth)
	if err != nil {
		return ImpactResult{}, fmt.Errorf("resolve callees of %q: %w", symbolName, err)
	}
	return ImpactResult{Symbol: symbol, Callers: callers, Callees: callees}, nil
}
