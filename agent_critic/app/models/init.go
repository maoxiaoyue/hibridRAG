package models

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// AutoMigrate 用 Bun CreateTable IfNotExists 建立評估紀錄表
func AutoMigrate(db *bun.DB) error {
	ctx := context.Background()
	tables := []interface{}{
		(*CriticEvaluation)(nil),
	}
	for _, m := range tables {
		if _, err := db.NewCreateTable().Model(m).IfNotExists().Exec(ctx); err != nil {
			return fmt.Errorf("migrate %T: %w", m, err)
		}
	}
	return nil
}
