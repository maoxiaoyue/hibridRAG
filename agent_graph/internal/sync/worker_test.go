package sync

import (
	"testing"

	"agent_graph/app/config"
)

func TestNewWorker_DefaultsConfidenceThreshold(t *testing.T) {
	w := NewWorker(config.VaultConfig{}, nil, nil, nil, nil, 0)
	if w.confidenceThreshold != 0.8 {
		t.Errorf("confidenceThreshold = %v, want default 0.8", w.confidenceThreshold)
	}
	w2 := NewWorker(config.VaultConfig{}, nil, nil, nil, nil, 0.6)
	if w2.confidenceThreshold != 0.6 {
		t.Errorf("confidenceThreshold = %v, want explicit 0.6", w2.confidenceThreshold)
	}
}

func TestExtSet(t *testing.T) {
	set := extSet([]string{".MD", ".txt"})
	if !set[".md"] || !set[".txt"] {
		t.Errorf("extSet should lowercase entries, got %v", set)
	}
	if set[".png"] {
		t.Error("extSet should not contain unrelated extensions")
	}
}

func TestSHA256HexStableAndDistinct(t *testing.T) {
	a := sha256Hex([]byte("hello"))
	b := sha256Hex([]byte("hello"))
	c := sha256Hex([]byte("world"))
	if a != b {
		t.Error("same input should hash identically")
	}
	if a == c {
		t.Error("different input should hash differently")
	}
	if len(a) != 64 {
		t.Errorf("hex sha256 length = %d, want 64", len(a))
	}
}

func TestNewProposalIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newProposalID()
		if seen[id] {
			t.Fatalf("duplicate proposal id generated: %s", id)
		}
		seen[id] = true
	}
}
