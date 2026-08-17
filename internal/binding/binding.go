package binding

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"gateway/internal/idgen"
)

const (
	StateBound       = "bound"
	StateMissing     = "binding_missing"
	StateQuarantined = "quarantined"
)

type Binding struct {
	ID                string `json:"id"`
	SessionID         string `json:"session_id"`
	ConversationID    string `json:"conversation_id"`
	TeamID            string `json:"team_id"`
	AgentID           string `json:"agent_id,omitempty"`
	IdentityCardID    string `json:"identity_card_id,omitempty"`
	UpstreamChannelID string `json:"upstream_channel_id,omitempty"`
	BindingVersion    int    `json:"binding_version"`
	State             string `json:"binding_state"`
	Source            string `json:"binding_source"`
	LastResolvedAt    string `json:"last_resolved_at"`
}

type ResolveRequest struct {
	ConversationID    string
	APIKeyID          string
	TeamID            string
	AgentID           string
	IdentityCardID    string
	UpstreamChannelID string
	BindingSource     string
}

type Resolver struct{ database *sql.DB }

func NewResolver(database *sql.DB) *Resolver { return &Resolver{database: database} }

// Resolve re-reads the binding from SQLite on every call. The first call can
// derive team/agent/identity from the authenticated API key; later calls reuse
// the same binding version unless a previously missing binding is completed.
func (r *Resolver) Resolve(ctx context.Context, request ResolveRequest) (*Binding, error) {
	if r == nil || r.database == nil {
		return nil, fmt.Errorf("binding database is nil")
	}
	if request.ConversationID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if request.BindingSource == "" {
		request.BindingSource = "service"
	}
	conn, err := r.database.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire binding connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("begin binding transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	teamID, agentID, identityCardID := request.TeamID, request.AgentID, request.IdentityCardID
	if request.APIKeyID != "" {
		var keyTeam, owner string
		now := time.Now().UTC().Format(time.RFC3339Nano)
		err := conn.QueryRowContext(ctx, `SELECT COALESCE(team_id,''), COALESCE(owner_id,'')
			FROM api_keys
			WHERE id = ? AND enabled = 1
			  AND (revoked_at IS NULL OR revoked_at = '')
			  AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)`, request.APIKeyID, now).Scan(&keyTeam, &owner)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("API key not found")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve API key: %w", err)
		}
		if teamID != "" && keyTeam != "" && teamID != keyTeam {
			return nil, fmt.Errorf("API key team mismatch")
		}
		teamID = keyTeam
		if agentID == "" {
			agentID = owner
		}
	}
	if request.UpstreamChannelID != "" {
		var channelTeam string
		var enabled int
		err := conn.QueryRowContext(ctx, `SELECT COALESCE(team_id,''), enabled FROM upstream_channels WHERE id = ?`, request.UpstreamChannelID).Scan(&channelTeam, &enabled)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("upstream channel not found")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve upstream channel: %w", err)
		}
		if enabled != 1 {
			return nil, fmt.Errorf("upstream channel is disabled")
		}
		if teamID != "" && channelTeam != "" && teamID != channelTeam {
			return nil, fmt.Errorf("upstream channel team mismatch")
		}
		if teamID == "" {
			teamID = channelTeam
		}
	}
	if agentID != "" {
		var agentTeam, agentCard string
		err := conn.QueryRowContext(ctx, `SELECT COALESCE(team_id,''), COALESCE(identity_card_id,'') FROM agents WHERE id = ?`, agentID).Scan(&agentTeam, &agentCard)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("agent not found")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve agent: %w", err)
		}
		if teamID != "" && agentTeam != "" && teamID != agentTeam {
			return nil, fmt.Errorf("agent team mismatch")
		}
		if teamID == "" {
			teamID = agentTeam
		}
		if identityCardID == "" {
			identityCardID = agentCard
		}
	}
	if identityCardID != "" {
		var cardTeam, cardAgent, cardStatus string
		err := conn.QueryRowContext(ctx, `SELECT COALESCE(team_id,''), COALESCE(agent_id,''), COALESCE(status,'') FROM identity_cards WHERE id = ?`, identityCardID).Scan(&cardTeam, &cardAgent, &cardStatus)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("identity card not found")
		}
		if err != nil {
			return nil, fmt.Errorf("resolve identity card: %w", err)
		}
		if cardStatus != "" && cardStatus != "active" {
			return nil, fmt.Errorf("identity card is not active")
		}
		if teamID != "" && cardTeam != "" && teamID != cardTeam {
			return nil, fmt.Errorf("identity card team mismatch")
		}
		if agentID != "" && cardAgent != "" && agentID != cardAgent {
			return nil, fmt.Errorf("identity card agent mismatch")
		}
		if teamID == "" {
			teamID = cardTeam
		}
		if agentID == "" {
			agentID = cardAgent
		}
	}
	if agentID != "" && identityCardID == "" {
		_ = conn.QueryRowContext(ctx, `SELECT COALESCE(identity_card_id,'') FROM agents WHERE id = ?`, agentID).Scan(&identityCardID)
	}
	if teamID != "" && request.UpstreamChannelID == "" {
		_ = conn.QueryRowContext(ctx, `SELECT id FROM upstream_channels WHERE team_id = ? AND enabled = 1 ORDER BY priority, id LIMIT 1`, teamID).Scan(&request.UpstreamChannelID)
	}

	var existing Binding
	var lastResolved string
	err = conn.QueryRowContext(ctx, `SELECT id, session_id, conversation_id, COALESCE(team_id,''), COALESCE(agent_id,''), COALESCE(identity_card_id,''), COALESCE(upstream_channel_id,''), binding_version, binding_state, binding_source, last_resolved_at FROM session_bindings WHERE conversation_id = ? ORDER BY binding_version DESC LIMIT 1`, request.ConversationID).Scan(
		&existing.ID, &existing.SessionID, &existing.ConversationID, &existing.TeamID, &existing.AgentID, &existing.IdentityCardID, &existing.UpstreamChannelID, &existing.BindingVersion, &existing.State, &existing.Source, &lastResolved)
	if err == nil {
		if teamID != "" && existing.TeamID != "" && teamID != existing.TeamID {
			return nil, fmt.Errorf("conversation is bound to another team")
		}
		if existing.State == StateBound {
			if agentID != "" && existing.AgentID != "" && agentID != existing.AgentID {
				return nil, fmt.Errorf("conversation is bound to another agent")
			}
			if identityCardID != "" && existing.IdentityCardID != "" && identityCardID != existing.IdentityCardID {
				return nil, fmt.Errorf("conversation is bound to another identity card")
			}
			if request.UpstreamChannelID != "" && existing.UpstreamChannelID != "" && request.UpstreamChannelID != existing.UpstreamChannelID {
				return nil, fmt.Errorf("conversation is bound to another upstream channel")
			}
		}
		// A binding that is still missing required routing information is a
		// durable quarantine record. Reuse it while the same information is
		// still unavailable instead of creating a new version on every retry.
		// Once both the team and upstream channel are known, the code below
		// creates the next version and transitions the binding to bound.
		if existing.State == StateBound || (existing.State == StateMissing && (teamID == "" || request.UpstreamChannelID == "")) {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			// A missing binding may gain information on a later request while
			// remaining quarantined. Persist only newly resolved fields; a bound
			// binding is immutable and is handled by the checks above.
			if existing.State == StateMissing {
				if _, err := conn.ExecContext(ctx, `UPDATE session_bindings SET team_id=COALESCE(team_id, ?), agent_id=COALESCE(agent_id, ?), identity_card_id=COALESCE(identity_card_id, ?), upstream_channel_id=COALESCE(upstream_channel_id, ?), last_resolved_at = ?, updated_at = ? WHERE id = ?`, nullable(teamID), nullable(agentID), nullable(identityCardID), nullable(request.UpstreamChannelID), now, now, existing.ID); err != nil {
					return nil, fmt.Errorf("refresh missing binding: %w", err)
				}
				if _, err := conn.ExecContext(ctx, `UPDATE sessions SET team_id=COALESCE(team_id, ?), agent_id=COALESCE(agent_id, ?), upstream_channel_id=COALESCE(upstream_channel_id, ?), updated_at=? WHERE id=?`, nullable(teamID), nullable(agentID), nullable(request.UpstreamChannelID), now, existing.SessionID); err != nil {
					return nil, fmt.Errorf("refresh missing session: %w", err)
				}
				if existing.TeamID == "" {
					existing.TeamID = teamID
				}
				if existing.AgentID == "" {
					existing.AgentID = agentID
				}
				if existing.IdentityCardID == "" {
					existing.IdentityCardID = identityCardID
				}
				if existing.UpstreamChannelID == "" {
					existing.UpstreamChannelID = request.UpstreamChannelID
				}
			} else if _, err := conn.ExecContext(ctx, `UPDATE session_bindings SET last_resolved_at = ?, updated_at = ? WHERE id = ?`, now, now, existing.ID); err != nil {
				return nil, fmt.Errorf("refresh binding: %w", err)
			}
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				return nil, fmt.Errorf("commit binding refresh: %w", err)
			}
			committed = true
			existing.LastResolvedAt = now
			return &existing, nil
		}
	}
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query existing binding: %w", err)
	}

	state := StateBound
	if teamID == "" || request.UpstreamChannelID == "" {
		state = StateMissing
	}
	sessionID := existing.SessionID
	if sessionID == "" {
		sessionID = idgen.NewID()
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := conn.ExecContext(ctx, `INSERT OR IGNORE INTO sessions (id, team_id, agent_id, upstream_channel_id, kind, conversation_id, binding_version, status, created_at, updated_at) VALUES (?, ?, ?, ?, 'main', ?, 0, 'active', ?, ?)`, sessionID, nullable(teamID), nullable(agentID), nullable(request.UpstreamChannelID), request.ConversationID, now, now); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	var version int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(binding_version), 0) + 1 FROM session_bindings WHERE conversation_id = ?`, request.ConversationID).Scan(&version); err != nil {
		return nil, fmt.Errorf("next binding version: %w", err)
	}
	bindingID := idgen.NewID()
	if _, err := conn.ExecContext(ctx, `INSERT INTO session_bindings (id, session_id, conversation_id, team_id, agent_id, identity_card_id, upstream_channel_id, binding_version, binding_state, binding_source, last_resolved_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, bindingID, sessionID, request.ConversationID, nullable(teamID), nullable(agentID), nullable(identityCardID), nullable(request.UpstreamChannelID), version, state, request.BindingSource, now, now, now); err != nil {
		return nil, fmt.Errorf("insert binding: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE sessions SET team_id = ?, agent_id = ?, upstream_channel_id = ?, binding_version = ?, updated_at = ? WHERE id = ?`, nullable(teamID), nullable(agentID), nullable(request.UpstreamChannelID), version, now, sessionID); err != nil {
		return nil, fmt.Errorf("update session binding: %w", err)
	}
	if state == StateMissing {
		payload, _ := json.Marshal(map[string]string{"conversation_id": request.ConversationID, "session_id": sessionID, "binding_id": bindingID})
		if _, err := conn.ExecContext(ctx, `INSERT INTO jobs (id, queue, priority, team_id, agent_id, payload_json, partition_key, max_retries) VALUES (?, 'binding_fix', 90, ?, ?, ?, ?, 5)`, idgen.NewID(), nullable(teamID), nullable(agentID), string(payload), "binding:"+request.ConversationID); err != nil {
			return nil, fmt.Errorf("enqueue binding repair: %w", err)
		}
	} else if existing.State == StateMissing {
		// A previously quarantined binding has now resolved. Retire pending
		// repair work so a stale worker cannot rewrite the historical binding.
		_, _ = conn.ExecContext(ctx, `UPDATE jobs SET status='done', finished_at=? WHERE queue='binding_fix' AND status IN ('pending','processing') AND json_extract(payload_json,'$.conversation_id')=?`, now, request.ConversationID)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("commit binding: %w", err)
	}
	committed = true
	return &Binding{ID: bindingID, SessionID: sessionID, ConversationID: request.ConversationID, TeamID: teamID, AgentID: agentID, IdentityCardID: identityCardID, UpstreamChannelID: request.UpstreamChannelID, BindingVersion: version, State: state, Source: request.BindingSource, LastResolvedAt: now}, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
