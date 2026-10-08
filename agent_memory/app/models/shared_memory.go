package models

import (
	"time"

	"github.com/uptrace/bun"
)

// SharedMemory 跨 agent 協調用的 KV pool；以 pool_id（而非 agent_id）隔離。
// 任何知道 pool_id 的 agent 都能讀寫，用來交換多 agent 協作時的共用狀態
// （例如同一個任務的多個 agent 需要看到彼此的中間結果）。
type SharedMemory struct {
	bun.BaseModel `bun:"table:shared_memory,alias:sm"`

	ID        string                 `bun:"id,pk" json:"id"`
	PoolID    string                 `bun:"pool_id,notnull,unique:uq_shared_memory_pool_key" json:"pool_id"`
	Key       string                 `bun:"key,notnull,unique:uq_shared_memory_pool_key" json:"key"`
	Value     map[string]interface{} `bun:"value,type:jsonb,notnull" json:"value"`
	WrittenBy string                 `bun:"written_by" json:"written_by,omitempty"`
	CreatedAt time.Time              `bun:"created_at,nullzero,notnull,default:current_timestamp" json:"created_at"`
	UpdatedAt time.Time              `bun:"updated_at,nullzero,notnull,default:current_timestamp" json:"updated_at"`
}

// UpsertSharedMemoryRequest 以 (pool_id, key) 為鍵執行 upsert
type UpsertSharedMemoryRequest struct {
	PoolID    string                 `json:"pool_id" binding:"required"`
	Key       string                 `json:"key" binding:"required"`
	Value     map[string]interface{} `json:"value" binding:"required"`
	WrittenBy string                 `json:"written_by,omitempty"`
}
