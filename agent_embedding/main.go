package main

import (
	"fmt"
	"os"

	"agent_embedding/app/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
