package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/garybowers/cineva/server/internal/version"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Cineva version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("cineva", version.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
