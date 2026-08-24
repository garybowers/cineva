package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:           "cineva",
	Short:         "Cineva — self-hosted media suite for movies, TV, music, audiobooks, podcasts, and books",
	SilenceUsage:  true,
	SilenceErrors: true,
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
}

func initConfig() {
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 4747)
	viper.SetDefault("database.path", "cineva.db")
	viper.SetDefault("libraries.artwork_dir", "artwork")
	viper.SetDefault("metadata.tmdb.api_key", "")
	viper.SetDefault("transcode.dir", filepath.Join(os.TempDir(), "cineva-transcode"))
	viper.SetDefault("transcode.segment_secs", 6)

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
