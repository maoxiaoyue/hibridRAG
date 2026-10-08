// 對話回合制引擎（手機版）：送訊息 → 拿到 final 回覆。
//
// 手機版不移植本機工具 / 內建 Coding Agent，因此不送 client_tools；
// 正常情況 server 不會回 tool_calls。防禦性處理：若 server 仍回 tool_calls
// （例如後端行為改變），逐一回「此裝置不支援本機工具」的錯誤結果並續推理，
// 讓 LLM 能收斂到純文字回答，而不是整輪對話卡死。
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"mobile_client/app/models"
)

// NewSessionID 產生一個 client 端 session id。新對話首輪由 client 先產生（而非等 server
// 指派），狀態輪詢（ChatStatus）才有 key 可查——server 收到非空 session_id 會沿用。
func NewSessionID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "sess_local"
	}
	return "sess_" + hex.EncodeToString(b[:])
}

// StatusFunc 用來回報中間進度。
type StatusFunc func(msg string)

// ChatBackend 對話後端的最小介面——由 GatewayClient（gRPC → agent_gateway）滿足。
type ChatBackend interface {
	Chat(ctx context.Context, req models.ChatRequest) (*models.ChatResponse, error)
	AgentID() string
}

// ChatEngine 持有一次對話 session 的狀態。
type ChatEngine struct {
	client             ChatBackend
	sessionID          string
	preferredSessionID string  // 首輪起始 session id（client 產生，供狀態輪詢）
	temperature        float32 // 0 = 不送（用 server 預設）
	lastUsage          *models.Usage
	status             StatusFunc
}

// NewChatEngine 建立引擎。status 可為 nil。
func NewChatEngine(client ChatBackend, status StatusFunc) *ChatEngine {
	return &ChatEngine{client: client, status: status}
}

// SessionID 回傳目前 session id（首輪後由 server 指派）。
func (e *ChatEngine) SessionID() string { return e.sessionID }

// SetSession 設定既有 session id（跨多次送出延續同一對話脈絡）。
func (e *ChatEngine) SetSession(id string) { e.sessionID = id }

// SetPreferredSessionID 設定新對話首輪要用的 session id（client 端產生）。
// 只在 sessionID 為空（新對話）時生效。
func (e *ChatEngine) SetPreferredSessionID(id string) { e.preferredSessionID = id }

// SetTemperature 設定本輪對話的取樣溫度；0 表示不送、用 server 預設。
func (e *ChatEngine) SetTemperature(t float32) { e.temperature = t }

// Usage 回傳最近一次 final 回覆的 token 用量（server 沒回時為 nil）。
func (e *ChatEngine) Usage() *models.Usage { return e.lastUsage }

// Send 送一則使用者訊息，回傳終局 assistant 文字。
func (e *ChatEngine) Send(ctx context.Context, userText string) (string, error) {
	sid := e.sessionID
	if sid == "" {
		sid = e.preferredSessionID
	}
	req := models.ChatRequest{
		AgentID:     e.client.AgentID(),
		SessionID:   sid,
		Messages:    []models.Msg{{Role: "user", Content: userText}},
		Temperature: e.temperature,
	}

	const maxRounds = 12
	for round := 0; round < maxRounds; round++ {
		resp, err := e.client.Chat(ctx, req)
		if err != nil {
			return "", err
		}
		if e.sessionID == "" {
			e.sessionID = resp.SessionID
		}

		if resp.Status == "final" {
			e.lastUsage = resp.Usage
			return resp.Content, nil
		}
		// status == "tool_calls"：手機版不支援本機工具，逐一回錯讓 LLM 收斂。
		results := make([]models.ToolResult, 0, len(resp.ToolCalls))
		for _, call := range resp.ToolCalls {
			if e.status != nil {
				e.status(fmt.Sprintf("Declining unsupported local tool %s...", call.Name))
			}
			results = append(results, models.ToolResult{
				ToolCallID: call.ID, Name: call.Name,
				Content: "local tools are not supported on this mobile client; answer without the tool",
				IsError: true,
			})
		}
		req = models.ChatRequest{
			AgentID:        e.client.AgentID(),
			SessionID:      e.sessionID,
			ContinuationID: resp.ContinuationID,
			ToolResults:    results,
		}
	}
	return "", fmt.Errorf("%w (max=%d)", ErrChatRoundLimit, maxRounds)
}
