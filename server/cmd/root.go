package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/media"
)

var cfgFile string

// prettyLogs says whether logs are going to a person (a terminal) rather
// than a log collector; the serve command prints its startup card then.
var prettyLogs bool

var rootCmd = &cobra.Command{
	Use:           "cineva",
	Short:         "Cineva — self-hosted media suite for movies, TV, music, audiobooks, podcasts, and books",
	SilenceUsage:  true,
	SilenceErrors: true,
	// Logging is set up once the configuration is read, before any command
	// runs, so every command logs the same way.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		pretty, err := logging.Setup(viper.GetString("log.level"), viper.GetString("log.format"), os.Stderr)
		if err != nil {
			return err
		}
		prettyLogs = pretty
		media.RouteFFmpegLogs()
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ./cineva.yaml, then /etc/cineva/cineva.yaml)")
	rootCmd.PersistentFlags().String("log-level", "", "log level: debug, info, warn or error (default info)")
	_ = viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
}

// loadDotEnv reads KEY=VALUE lines from a git-ignored ./.env file into the
// environment (existing variables win), so secrets like the TMDB key can
// live outside both the repo and the yaml: CINEVA_METADATA_TMDB_API_KEY=...
func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func initConfig() {
	loadDotEnv()
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 4747)
	viper.SetDefault("database.path", "cineva.db")
	viper.SetDefault("libraries.artwork_dir", "artwork")
	// Empty default: metadata.APIKey falls back to the built-in project key
	// when nothing overrides it.
	viper.SetDefault("metadata.tmdb.api_key", "")
	viper.SetDefault("metadata.musicbrainz.enabled", true)
	viper.SetDefault("transcode.dir", filepath.Join(os.TempDir(), "cineva-transcode"))
	viper.SetDefault("transcode.segment_secs", 6)
	viper.SetDefault("transcode.hwaccel", "auto")
	viper.SetDefault("metadata.cert_region", "GB")
	viper.SetDefault("libraries.scan_interval", "15m")
	viper.SetDefault("libraries.history_retention_days", 90)
	viper.SetDefault("libraries.intro_detection", true)
	viper.SetDefault("transcode.max_bytes", int64(media.DefaultTranscodeMaxBytes))
	viper.SetDefault("stats.sample_secs", 5)
	viper.SetDefault("stats.retention_hours", 24)
	viper.SetDefault("auth.session_days", 30)
	viper.SetDefault("auth.secure_cookies", false)
	viper.SetDefault("auth.login_mode", "picker")
	viper.SetDefault("log.level", "info")
	viper.SetDefault("log.format", "auto")

	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("cineva")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/cineva")
	}

	viper.SetEnvPrefix("CINEVA")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
	}
}
