package acl

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	VisibilityPrivate    = "private"
	VisibilityTeam       = "team"
	VisibilityRestricted = "restricted"
	VisibilityAgent      = "agent"
)

type Subject struct {
	TeamID         string
	AgentID        string
	UserID         string
	Role           string
	IdentityCardID string
}

type Resource struct {
	ID             string
	TeamID         string
	IdentityCardID string
	Visibility     string
}

// CanRead applies the team boundary first, then the visibility policy and
// finally explicit acl_entries for restricted resources.
func CanRead(ctx context.Context, database *sql.DB, resource Resource, subject Subject) (bool, error) {
	if resource.TeamID == "" || subject.TeamID == "" || resource.TeamID != subject.TeamID {
		return false, nil
	}
	switch resource.Visibility {
	case "", VisibilityTeam:
		return true, nil
	case VisibilityPrivate:
		return resource.IdentityCardID != "" && resource.IdentityCardID == subject.IdentityCardID, nil
	case VisibilityAgent:
		// Agent-scoped assets are attached to an identity card. The caller's
		// runtime agent id is not the identity-card id stored on the asset.
		return resource.IdentityCardID != "" && resource.IdentityCardID == subject.IdentityCardID, nil
	case VisibilityRestricted:
		if database == nil {
			return false, fmt.Errorf("restricted ACL database is nil")
		}
		rows, err := database.QueryContext(ctx, `SELECT grantee_type, grantee_id FROM acl_entries WHERE asset_id = ? AND permission IN ('read','write','manage')`, resource.ID)
		if err != nil {
			return false, fmt.Errorf("query ACL entries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var kind, id string
			if err := rows.Scan(&kind, &id); err != nil {
				return false, err
			}
			switch kind {
			case "team":
				if id == subject.TeamID {
					return true, nil
				}
			case "agent":
				if id != "" && id == subject.AgentID {
					return true, nil
				}
			case "user":
				if id != "" && id == subject.UserID {
					return true, nil
				}
			case "role":
				if id != "" && id == subject.Role {
					return true, nil
				}
			}
		}
		return false, rows.Err()
	default:
		return false, fmt.Errorf("unknown visibility %q", resource.Visibility)
	}
}
