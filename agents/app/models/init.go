package models

import (
	"context"
	"fmt"

	"agent/internal/database"
	"github.com/uptrace/bun"
)

// AutoMigrate 自動遷移所有模型（使用 CreateTable IfNotExists）
func AutoMigrate(db *bun.DB) error {
	ctx := context.Background()

	// PersonalMemory/ProceduralMemory/EpisodicMemory/ToolMemory 的表結構
	// 現在由 agent_memory 服務擁有並自行 AutoMigrate；本服務只透過 gRPC
	// 呼叫 agent_memory，不再直連 Postgres 建這幾張記憶表。
	models := []interface{}{
		(*IngestJob)(nil),
	}

	for _, model := range models {
		if _, err := db.NewCreateTable().Model(model).IfNotExists().Exec(ctx); err != nil {
			return fmt.Errorf("failed to create table for %T: %w", model, err)
		}
	}

	return nil
}

// GetDB 獲取 HypDB 數據庫實例
func GetDB() *bun.DB {
	return database.GetDB()
}
