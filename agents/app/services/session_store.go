// session_store.go — short-term/working memory: a real multi-turn
// conversation-history buffer for the sync chat path (POST /api/chat).
//
// Before this, each request was effectively single-turn from the server's
// perspective: the client only ever sent session_id + the newest user
// message (see agent_client/app/services/chat_service.go), and the only
// server-side state was chatCont's in-memory, single-round-trip tool-loop
// continuation (agent_chat.go's chatContMap, 30-min TTL, one process only).
// SessionStore fixes the actual gap: prior turns, persisted in Redis
// (survives process restarts / multiple replicas, unlike chatContMap),
// keyed by agent_id+session_id, loaded at the start of every new turn and
// appended to after every final response.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionStore persists conversation turns (role+content only — no tool-call
// plumbing, which is chatCont's ephemeral, single-round-trip concern).
type SessionStore struct {
	rdb      *redis.Client
	maxTurns int           // max messages retained (trims oldest first)
	ttl      time.Duration // refreshed on every Append
}

// NewSessionStore returns nil if rdb is nil — session history is optional
// (falls back to today's single-turn-per-request behavior).
func NewSessionStore(rdb *redis.Client, maxTurns int, ttl time.Duration) *SessionStore {
	if rdb == nil {
		return nil
	}
	if maxTurns <= 0 {
		maxTurns = 40
	}
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	return &SessionStore{rdb: rdb, maxTurns: maxTurns, ttl: ttl}
}

func sessionKey(agentID, sessionID string) string {
	return "chat:session:" + agentID + ":" + sessionID
}

// Load returns prior turns oldest-first. A missing/empty session returns
// (nil, nil) — not an error. Nil-receiver-safe.
func (s *SessionStore) Load(ctx context.Context, agentID, sessionID string) ([]ChatMessage, error) {
	if s == nil || sessionID == "" {
		return nil, nil
	}
	raw, err := s.rdb.Get(ctx, sessionKey(agentID, sessionID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("session store load: %w", err)
	}
	var msgs []ChatMessage
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return nil, fmt.Errorf("session store load: decode: %w", err)
	}
	return msgs, nil
}

// Append adds new turns, trims to maxTurns (drops oldest), and refreshes TTL.
// Nil-receiver-safe (matches the best-effort write pattern used by episodic
// memory elsewhere in this codebase).
func (s *SessionStore) Append(ctx context.Context, agentID, sessionID string, msgs ...ChatMessage) error {
	if s == nil || sessionID == "" || len(msgs) == 0 {
		return nil
	}
	prior, err := s.Load(ctx, agentID, sessionID)
	if err != nil {
		return err
	}
	all := append(prior, msgs...)
	if len(all) > s.maxTurns {
		all = all[len(all)-s.maxTurns:]
	}
	buf, err := json.Marshal(all)
	if err != nil {
		return fmt.Errorf("session store append: encode: %w", err)
	}
	if err := s.rdb.Set(ctx, sessionKey(agentID, sessionID), buf, s.ttl).Err(); err != nil {
		return fmt.Errorf("session store append: %w", err)
	}
	return nil
}
