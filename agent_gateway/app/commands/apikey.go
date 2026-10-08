// apikey.go — dev_pub_0.9 API Key 管理 CLI（只能在部署機上執行，不走網路 API）。
//
//	gateway apikey create --name <who>   簽發新 key（明文只印這一次）
//	gateway apikey list                  列出全部 key（不含明文/hash）
//	gateway apikey revoke <id>           撤銷
//	gateway apikey unbind <id>           重置裝置綁定（換機用）
package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	appconfig "agent_gatway/app/config"
	"agent_gatway/app/models"
	"agent_gatway/app/services"
	"agent_gatway/internal/db"
)

var apikeyName string

func init() {
	apikeyCmd := &cobra.Command{
		Use:   "apikey",
		Short: "Manage client API keys (create/list/revoke/unbind)",
	}

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Issue a new API key (plaintext printed exactly once)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withKeyService(func(ctx context.Context, svc *services.APIKeyService) error {
				plaintext, rec, err := svc.Create(ctx, apikeyName)
				if err != nil {
					return err
				}
				fmt.Printf("id:      %s\n", rec.ID)
				fmt.Printf("name:    %s\n", rec.Name)
				fmt.Printf("api key: %s\n", plaintext)
				fmt.Println("\n明文只顯示這一次，請立刻保存；DB 只存 hash，遺失需重發。")
				return nil
			})
		},
	}
	createCmd.Flags().StringVar(&apikeyName, "name", "", "who/what this key is issued to (required)")
	_ = createCmd.MarkFlagRequired("name")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withKeyService(func(ctx context.Context, svc *services.APIKeyService) error {
				keys, err := svc.List(ctx)
				if err != nil {
					return err
				}
				if len(keys) == 0 {
					fmt.Println("(no api keys)")
					return nil
				}
				fmt.Printf("%-22s %-16s %-8s %-14s %s\n", "ID", "NAME", "STATUS", "DEVICE", "LAST USED")
				for _, k := range keys {
					device := "(unbound)"
					if k.DeviceID != "" {
						device = k.DeviceID
						if len(device) > 12 {
							device = device[:12] + "…"
						}
					}
					last := "-"
					if !k.LastUsedAt.IsZero() {
						last = k.LastUsedAt.Format(time.RFC3339)
					}
					fmt.Printf("%-22s %-16s %-8s %-14s %s\n", k.ID, k.Name, k.Status, device, last)
				}
				return nil
			})
		},
	}

	revokeCmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withKeyService(func(ctx context.Context, svc *services.APIKeyService) error {
				if err := svc.Revoke(ctx, args[0]); err != nil {
					return err
				}
				fmt.Printf("revoked %s\n", args[0])
				return nil
			})
		},
	}

	unbindCmd := &cobra.Command{
		Use:   "unbind <id>",
		Short: "Reset device binding (for device replacement)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withKeyService(func(ctx context.Context, svc *services.APIKeyService) error {
				if err := svc.Unbind(ctx, args[0]); err != nil {
					return err
				}
				fmt.Printf("unbound %s (next use re-binds to the calling device)\n", args[0])
				return nil
			})
		},
	}

	apikeyCmd.AddCommand(createCmd, listCmd, revokeCmd, unbindCmd)
	rootCmd.AddCommand(apikeyCmd)
}

// withKeyService 載入 config → 開 DB → 建表（IfNotExists）→ 跑 fn → 收連線。
func withKeyService(fn func(context.Context, *services.APIKeyService) error) error {
	cfg, err := appconfig.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Database.DSN == "" {
		return fmt.Errorf("database.dsn is not configured (api keys live in PG)")
	}
	if _, err := db.Init(&cfg.Database); err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := models.AutoMigrateAPIKeys(ctx, db.DB()); err != nil {
		return fmt.Errorf("migrate api_keys: %w", err)
	}
	return fn(ctx, services.NewAPIKeyService(db.DB()))
}
