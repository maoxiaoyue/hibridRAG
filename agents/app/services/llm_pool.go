// llm_pool.go — 依 model 名稱快取/延遲建構 LLMClient，讓不同 persona 可以在
// 同一個 agents process 內跑在不同的 Ollama model 上，而不需要為每個 model
// 重新走一次設定載入流程。
//
// 只支援 Ollama provider（目前唯一端到端接通多模型切換的 provider）；
// anthropic/openai/custom 模式維持單一全域 LLMClient，不進這個池。
package services

import "sync"

// LLMClientPool 共用同一份 base OllamaConfig 樣板（URL/Timeout/MaxTokens
// 不變），只有 Model 隨 Get() 呼叫變化。安全併發使用。
//
// 規模假設：一個 process 服務的 persona 數量有限（幾個到幾十個），故用單純
// map 快取、無 LRU 淘汰機制——不需要為此複雜化。
type LLMClientPool struct {
	base OllamaConfig // 樣板；Model/Role 欄位會被 Get() 覆寫，故其值不重要

	mu      sync.RWMutex
	clients map[string]LLMClient
}

// NewLLMClientPool 以 baseCfg 為樣板建立 pool（URL/Timeout/MaxTokens 沿用，
// Model/Role 由每次 Get() 呼叫指定）。
func NewLLMClientPool(baseCfg OllamaConfig) *LLMClientPool {
	return &LLMClientPool{
		base:    baseCfg,
		clients: make(map[string]LLMClient),
	}
}

// Get 回傳 modelName 對應的 LLMClient，第一次呼叫時建構並快取。
// NewOllamaClient 純粹是 struct 建構（無 I/O、無 dial），故並行對同一個
// 從未見過的 modelName 呼叫 Get 時，可能各自建出一個實例、只有一個會留在
// cache 裡——這是可接受的重複建構，每個呼叫端仍會拿到一個可正常運作的 client。
func (p *LLMClientPool) Get(modelName string) LLMClient {
	p.mu.RLock()
	if c, ok := p.clients[modelName]; ok {
		p.mu.RUnlock()
		return c
	}
	p.mu.RUnlock()

	cfg := p.base
	cfg.Model = modelName
	cfg.Role = "" // 已經解析過，不需要 NewOllamaClient/registry 再次解析
	client := NewOllamaClient(cfg)

	p.mu.Lock()
	p.clients[modelName] = client
	p.mu.Unlock()
	return client
}
