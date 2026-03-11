package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "loadgen",
	Short: "PostgreSQL load generator for autoscaling research",
	Long: `loadgen drives configurable transaction workloads against a PostgreSQL
database and reports TPS and latency percentiles in real time.`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
