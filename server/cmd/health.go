package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// health asks a running server whether it is up, for container health
// checks: the image carries no curl, and a check that needs one would
// have to install it for nothing else. Exit 0 when /api/v1/health answers
// 200, 1 otherwise, with the reason on stderr.
var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Check a running server answers (exit 0 when it does)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		url, _ := cmd.Flags().GetString("url")
		if url == "" {
			url = fmt.Sprintf("http://127.0.0.1:%d", viper.GetInt("server.port"))
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/v1/health", nil)
		if err != nil {
			return err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "not healthy:", err)
			os.Exit(1)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			fmt.Fprintln(os.Stderr, "not healthy: status", res.StatusCode)
			os.Exit(1)
		}
		fmt.Println("healthy")
		return nil
	},
}

func init() {
	healthCmd.Flags().String("url", "", "the server's address (default: this configuration's port on localhost)")
	rootCmd.AddCommand(healthCmd)
}
