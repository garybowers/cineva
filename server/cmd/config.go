package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration commands",
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Check that the configuration loads and print the effective values",
	RunE: func(cmd *cobra.Command, args []string) error {
		if used := viper.ConfigFileUsed(); used != "" {
			fmt.Println("config file:", used)
		} else {
			fmt.Println("config file: none found (using defaults + CINEVA_* env)")
		}
		for _, key := range viper.AllKeys() {
			fmt.Printf("%s = %v\n", key, viper.Get(key))
		}
		return nil
	},
}

func init() {
	configCmd.AddCommand(configValidateCmd)
	rootCmd.AddCommand(configCmd)
}
