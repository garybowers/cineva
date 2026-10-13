package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/garybowers/cineva/server/internal/library"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// newManager builds a scan manager, wiring TMDB in when a key is available —
// the configured override if there is one, else the built-in project key.
func newManager(st *store.Store, artworkDir string) *library.Manager {
	var video metadata.VideoProvider
	if key, ok := metadata.APIKey(viper.GetString("metadata.tmdb.api_key")); ok {
		video = metadata.NewTMDB(key)
	} else {
		logging.For("metadata").Warn("no TMDB key: titles carry only what the files themselves hold",
			"fix", "set metadata.tmdb.api_key, CINEVA_METADATA_TMDB_API_KEY or --tmdb-key")
	}
	mgr := library.NewManager(st, video, artworkDir)
	if viper.GetBool("metadata.musicbrainz.enabled") {
		mgr.Music = metadata.NewMusicBrainz()
	}
	return mgr
}

// addTMDBKeyFlag registers --tmdb-key on a command. The flag is the top of
// the usual precedence (flag > env > config > built-in default), so it is
// the way to try a different key without editing anything.
func addTMDBKeyFlag(cmd *cobra.Command) {
	cmd.Flags().String("tmdb-key", "", "TMDB API key, overriding the built-in one")
	_ = viper.BindPFlag("metadata.tmdb.api_key", cmd.Flags().Lookup("tmdb-key"))
}

var scanCmd = &cobra.Command{
	Use:   "scan [library]",
	Short: "Scan a library (by name or id), or all libraries when none is given",
	RunE: func(cmd *cobra.Command, args []string) error {
		full, _ := cmd.Flags().GetBool("full")
		st, err := store.Open(viper.GetString("database.path"))
		if err != nil {
			return err
		}
		defer st.Close()
		ctx := context.Background()
		if err := st.Migrate(ctx); err != nil {
			return err
		}

		libs, err := st.Libraries(ctx)
		if err != nil {
			return err
		}
		if len(args) == 1 {
			filtered := libs[:0]
			for _, l := range libs {
				if l.ID == args[0] || l.Name == args[0] {
					filtered = append(filtered, l)
				}
			}
			if len(filtered) == 0 {
				return fmt.Errorf("no library named %q", args[0])
			}
			libs = filtered
		}
		if len(libs) == 0 {
			return fmt.Errorf("no libraries configured — create one in the admin UI")
		}

		manager := newManager(st, viper.GetString("libraries.artwork_dir"))
		for _, lib := range libs {
			fmt.Printf("scanning %s (%s)…\n", lib.Name, lib.MediaType)
			result, err := manager.RunScanSync(ctx, lib, full)
			if err != nil {
				return err
			}
			if result.Status == "failed" {
				fmt.Printf("  failed: %s\n", result.Error)
				continue
			}
			fmt.Printf("  %d files seen, %d items\n", result.FilesSeen, result.ItemsCreated)
		}
		return nil
	},
	Args: cobra.MaximumNArgs(1),
}

func init() {
	addTMDBKeyFlag(scanCmd)
	scanCmd.Flags().Bool("full", false, "reread and rematch every file, not just those changed since the last scan")
	rootCmd.AddCommand(scanCmd)
}
