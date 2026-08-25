package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/store"
)

// user add / user passwd — bootstrap the first admin without a browser, or
// reset a password from the console when someone is locked out.

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage user accounts",
}

var userAddCmd = &cobra.Command{
	Use:   "add <username>",
	Short: "Create a user (the first user created becomes the admin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		ctx := context.Background()

		role := store.RoleUser
		if admin, _ := cmd.Flags().GetBool("admin"); admin {
			role = store.RoleAdmin
		}
		n, err := st.CountUsers(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			role = store.RoleAdmin // someone has to be
		}
		password, _ := cmd.Flags().GetString("password")
		if password == "" {
			if password, err = promptPassword(true); err != nil {
				return err
			}
		}
		if err := auth.CheckPasswordPolicy(password); err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		display, _ := cmd.Flags().GetString("name")
		u, err := st.CreateUser(ctx, args[0], display, hash, role)
		if err != nil {
			return err
		}
		if n == 0 {
			// Watch state recorded before accounts existed belongs to the
			// first admin.
			if err := st.AdoptLocalState(ctx, u.ID); err != nil {
				return err
			}
		}
		fmt.Printf("created %s %q (%s)\n", u.Role, u.Username, u.ID)
		return nil
	},
}

var userPasswdCmd = &cobra.Command{
	Use:   "passwd <username>",
	Short: "Set a user's password (and sign out their browser sessions)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		ctx := context.Background()
		u, err := st.UserByUsername(ctx, args[0])
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no user %q", args[0])
		}
		if err != nil {
			return err
		}
		password, _ := cmd.Flags().GetString("password")
		if password == "" {
			if password, err = promptPassword(true); err != nil {
				return err
			}
		}
		if err := auth.CheckPasswordPolicy(password); err != nil {
			return err
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		if err := st.SetPasswordHash(ctx, u.ID, hash); err != nil {
			return err
		}
		if err := st.DeleteUserSessions(ctx, u.ID); err != nil {
			return err
		}
		fmt.Printf("password updated for %q\n", u.Username)
		return nil
	},
}

func openStore() (*store.Store, error) {
	st, err := store.Open(viper.GetString("database.path"))
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// promptPassword reads a password without echo (twice when confirm is
// set); falls back to a plain line read when stdin is not a terminal.
func promptPassword(confirm bool) (string, error) {
	read := func(label string) (string, error) {
		fmt.Fprint(os.Stderr, label)
		if term.IsTerminal(int(syscall.Stdin)) {
			b, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Fprintln(os.Stderr)
			return string(b), err
		}
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}
	pw, err := read("Password: ")
	if err != nil {
		return "", err
	}
	if confirm {
		again, err := read("Confirm password: ")
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", fmt.Errorf("passwords do not match")
		}
	}
	return pw, nil
}

func init() {
	userAddCmd.Flags().Bool("admin", false, "create an admin account")
	userAddCmd.Flags().String("password", "", "password (prompted when omitted)")
	userAddCmd.Flags().String("name", "", "display name (defaults to the username)")
	userPasswdCmd.Flags().String("password", "", "new password (prompted when omitted)")
	userCmd.AddCommand(userAddCmd, userPasswdCmd)
	rootCmd.AddCommand(userCmd)
}
