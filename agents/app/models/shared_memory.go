package models

import "time"

// SharedMemory — 跨 agent 協調用的 KV pool；以 pool_id（而非 agent_id）隔離。
// 純 gRPC DTO（agent_memory 持有實際的 Postgres 存取）。
type SharedMemory struct {
	PoolID    string
	Key       string
	Value     map[string]interface{}
	WrittenBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertSharedMemoryRequest 以 (pool_id, key) 為鍵執行 upsert
type UpsertSharedMemoryRequest struct {
	PoolID    string
	Key       string
	Value     map[string]interface{}
	WrittenBy string
}
