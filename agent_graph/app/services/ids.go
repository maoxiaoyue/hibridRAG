package services

import (
	"crypto/rand"
	"encoding/hex"
)

// newID matches the newID(prefix) convention used in agent_memory/agent_critic.
func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
