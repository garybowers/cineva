package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/garybowers/cineva/server/internal/api"
	"github.com/garybowers/cineva/server/internal/store"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Cineva server",
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := store.Open(viper.GetString("database.path"))
		if err != nil {
			return err
		}
		defer st.Close()

		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			return err
		}
		identity, err := st.EnsureIdentity(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("server identity: %x…\n", identity.PublicKey[:8])

		return api.Serve(api.Config{
			Host: viper.GetString("server.host"),
			Port: viper.GetInt("server.port"),
		}, st)
	},
}

func init() {
	serveCmd.Flags().Int("port", 4747, "HTTP port to listen on")
	serveCmd.Flags().String("host", "0.0.0.0", "address to bind")
	_ = viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("server.host", serveCmd.Flags().Lookup("host"))
	rootCmd.AddCommand(serveCmd)
}
