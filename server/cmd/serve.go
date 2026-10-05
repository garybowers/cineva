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

		// Any scan still 'running' at startup belonged to a dead process.
		if n, err := st.FailOrphanedScans(ctx); err != nil {
			return err
		} else if n > 0 {
			fmt.Printf("marked %d orphaned scan(s) as failed\n", n)
		}

		artworkDir := viper.GetString("libraries.artwork_dir")
		return api.Serve(api.Config{
			Host:         viper.GetString("server.host"),
			Port:         viper.GetInt("server.port"),
			ArtworkDir:   artworkDir,
			TranscodeDir: viper.GetString("transcode.dir"),
			SegmentSecs:  viper.GetInt("transcode.segment_secs"),
			HWAccel:      viper.GetString("transcode.hwaccel"),
			CertRegion:   viper.GetString("metadata.cert_region"),
			TranscodeMax: viper.GetInt64("transcode.max_bytes"),
			SampleSecs:   viper.GetInt("stats.sample_secs"),
			SessionDays:  viper.GetInt("auth.session_days"),
			SecureCookie: viper.GetBool("auth.secure_cookies"),
			LoginMode:    viper.GetString("auth.login_mode"),
		}, st, newManager(st, artworkDir))
	},
}

func init() {
	serveCmd.Flags().Int("port", 4747, "HTTP port to listen on")
	serveCmd.Flags().String("host", "0.0.0.0", "address to bind")
	_ = viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("server.host", serveCmd.Flags().Lookup("host"))
	addTMDBKeyFlag(serveCmd)
	rootCmd.AddCommand(serveCmd)
}
