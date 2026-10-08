// chat_status.go — 記錄各 session「目前跑到混合 RAG 的哪一個階段（Agent 1-4）」，
// 供 client 每秒輪詢顯示（狀態列在秒數前顯示目前處理中的 Agent）。
//
// 純 in-memory；ChatSync/runChatLoop 在階段轉換時 Set，結束時 Clear。找不到（已結束
// 或過期）→ Get 回 active=false，client 據此顯示自己的本地階段（例如執行本地工具中）。
package services

import (
	"sync"
	"time"
)

// 混合 RAG 的階段標籤（顯示在 client 狀態列）。
const (
	StageContextBuilder = "Agent 1 · ContextBuilder" // 檢索 / 組 context
	StageToolLoop       = "Agent 2 · ToolLoop"       // LLM 生成 / 工具迴圈
	StageCritic         = "Agent 3 · Critic"         // 答案審核（目前僅 gRPC Inbound 路徑）
	StageGraph          = "Agent 4 · Graph"          // 知識圖譜查詢（graph_query 工具）
)

type stageEntry struct {
	stage     string
	updatedAt time.Time
}

// ChatStatusTracker session → 目前階段。方法皆容忍 nil receiver（功能未接線時無操作）。
type ChatStatusTracker struct {
	mu  sync.RWMutex
	m   map[string]stageEntry
	ttl time.Duration
}

func NewChatStatusTracker() *ChatStatusTracker {
	return &ChatStatusTracker{m: make(map[string]stageEntry), ttl: 5 * time.Minute}
}

// Set 記錄某 session 目前階段。
func (t *ChatStatusTracker) Set(sessionID, stage string) {
	if t == nil || sessionID == "" {
		return
	}
	t.mu.Lock()
	t.m[sessionID] = stageEntry{stage: stage, updatedAt: time.Now()}
	t.mu.Unlock()
}

// Clear 移除某 session 的階段紀錄（處理結束 / 暫停等 client 工具時呼叫）。
func (t *ChatStatusTracker) Clear(sessionID string) {
	if t == nil || sessionID == "" {
		return
	}
	t.mu.Lock()
	delete(t.m, sessionID)
	t.mu.Unlock()
}

// Get 回傳目前階段與是否 active（存在且未過期）。
func (t *ChatStatusTracker) Get(sessionID string) (string, bool) {
	if t == nil || sessionID == "" {
		return "", false
	}
	t.mu.RLock()
	e, ok := t.m[sessionID]
	t.mu.RUnlock()
	if !ok {
		return "", false
	}
	if t.ttl > 0 && time.Since(e.updatedAt) > t.ttl {
		return "", false
	}
	return e.stage, true
}
