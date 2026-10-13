package cmd

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/garybowers/cineva/server/internal/api"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
	"github.com/garybowers/cineva/server/internal/version"
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
		// The card comes before anything else is logged — the hardware
		// probe, the metadata key — so it heads the output.
		if prettyLogs {
			printCard(ctx, st)
		}
		identity, err := st.EnsureIdentity(ctx)
		if err != nil {
			return err
		}
		startLog.Debug("server identity", "key", fmt.Sprintf("%x", identity.PublicKey[:8]))

		// Any scan still 'running' at startup belonged to a dead process.
		if n, err := st.FailOrphanedScans(ctx); err != nil {
			return err
		} else if n > 0 {
			startLog.Info("marked scans from a previous run as failed", "count", n)
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
			ScanInterval: viper.GetDuration("libraries.scan_interval"),
			RetainDays:   viper.GetInt("libraries.history_retention_days"),
			IntroDetect:  viper.GetBool("libraries.intro_detection"),
			PreviewsDir:  viper.GetString("libraries.previews_dir"),
			TranscodeMax: viper.GetInt64("transcode.max_bytes"),
			SampleSecs:   viper.GetInt("stats.sample_secs"),
			StatsHours:   viper.GetInt("stats.retention_hours"),
			SessionDays:  viper.GetInt("auth.session_days"),
			SecureCookie: viper.GetBool("auth.secure_cookies"),
			LoginMode:    viper.GetString("auth.login_mode"),
			Ready:        announce,
		}, st, newManager(st, artworkDir))
	},
}

var startLog = logging.For("server")

// printCard prints the startup card for a person at a terminal.
func printCard(ctx context.Context, st *store.Store) {
	libraries := "no libraries yet"
	if libs, err := st.Libraries(ctx); err != nil {
		startLog.Warn("counting libraries for the startup card", "err", err)
	} else if len(libs) == 1 {
		libraries = "1 library"
	} else if len(libs) > 1 {
		libraries = fmt.Sprintf("%d libraries", len(libs))
	}
	config := "no config file"
	if f := viper.ConfigFileUsed(); f != "" {
		config = "config " + f
	}
	addr := net.JoinHostPort(viper.GetString("server.host"), viper.GetString("server.port"))
	logging.Card(os.Stderr, logging.Colour(os.Stderr), "cineva "+version.Version,
		"http://"+displayAddr(addr)+" · "+libraries,
		config+" · database "+viper.GetString("database.path")+" · log "+viper.GetString("log.level"))
}

// announce says the server is up. A person at a terminal has had the card
// already; a log collector gets one structured line once it is listening.
func announce(s api.Startup) {
	if prettyLogs {
		return
	}
	startLog.Info("cineva started", "version", version.Version, "url", "http://"+displayAddr(s.Addr),
		"transcode", s.HWAccel, "libraries", s.Libraries, "config", viper.ConfigFileUsed(),
		"log_level", viper.GetString("log.level"))
}

// displayAddr turns the bound address into one a person can open: a server
// listening on every interface is reachable at localhost from here.
func displayAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func init() {
	serveCmd.Flags().Int("port", 4747, "HTTP port to listen on")
	serveCmd.Flags().String("host", "0.0.0.0", "address to bind")
	_ = viper.BindPFlag("server.port", serveCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("server.host", serveCmd.Flags().Lookup("host"))
	addTMDBKeyFlag(serveCmd)
	rootCmd.AddCommand(serveCmd)
}
