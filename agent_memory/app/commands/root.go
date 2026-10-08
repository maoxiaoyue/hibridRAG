package commands

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "agent_memory",
	Short: "Agent_memory CLI tool",
	Long:  "Agent_memory is a CLI tool built with HypGo scaffold.",
}

// Execute 執行根命令
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// 全域 flags
	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file")
}
