package models

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// AutoMigrate 建立一般關聯表（同步狀態 + 待審關係）。
// 圖譜本身（Apache AGE）與實體向量（pgvector）走各自的 DDL，
// 見 app/services/age_schema.go 的 EnsureSchema。
func AutoMigrate(db *bun.DB) error {
	ctx := context.Background()
	tables := []interface{}{
		(*SyncState)(nil),
		(*ProposedRelation)(nil),
	}
	for _, m := range tables {
		if _, err := db.NewCreateTable().Model(m).IfNotExists().Exec(ctx); err != nil {
			return fmt.Errorf("migrate %T: %w", m, err)
		}
	}
	return nil
}
