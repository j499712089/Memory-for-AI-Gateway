package worker

import (
	"errors"
	"testing"
)

func TestIsTransientSQLiteBusy(t *testing.T) {
	for _, err := range []error{
		errors.New("database is locked (5) (SQLITE_BUSY)"),
		errors.New("begin failed job transaction: database table is locked"),
		errors.New("sqlite_locked"),
	} {
		if !isTransientSQLiteBusy(err) {
			t.Fatalf("expected transient lock: %v", err)
		}
	}
	for _, err := range []error{
		errors.New("invalid l1_refine payload"),
		errors.New("database schema is corrupt"),
	} {
		if isTransientSQLiteBusy(err) {
			t.Fatalf("permanent error classified as transient: %v", err)
		}
	}
}

func TestCodeGraphPartitionScopesRefineBatches(t *testing.T) {
	gotA := codeGraphPartition("team", "turn-a", "all")
	gotB := codeGraphPartition("team", "turn-b", "all")
	if gotA == gotB {
		t.Fatalf("refine turns must not share a codegraph partition: %q", gotA)
	}
	if gotA != "codegraph:team:turn-a:all" {
		t.Fatalf("unexpected refine partition: %q", gotA)
	}
	if got := codeGraphPartition("team", "", "repo"); got != "codegraph:team:repo" {
		t.Fatalf("direct repo partition changed unexpectedly: %q", got)
	}
}
