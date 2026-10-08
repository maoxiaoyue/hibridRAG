package commands

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "agent_critic",
	Short: "Agent_critic CLI tool",
	Long:  "Agent_critic is a CLI tool built with HypGo scaffold.",
}

// Execute 執行根命令
func Execute() error {
	return rootCmd.Execute()
}
