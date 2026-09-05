package cli

import (
	"encoding/json"
	"errors"
	"github.com/spf13/cobra"
	"net/http"
	"strconv"
	"time"
)

func newInvitationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "invitation", Short: "Manage one-time account invitations (administrator only)"}
	create := &cobra.Command{Use: "create <email>", Short: "Print a new invitation once as JSON", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		lifetime, _ := cmd.Flags().GetDuration("expires-in")
		if lifetime <= 0 || lifetime > 30*24*time.Hour {
			return errors.New("--expires-in must be positive and no greater than 720h")
		}
		var result json.RawMessage
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodPost, "/admin/invitations", map[string]any{"email": args[0], "expires_at": time.Now().UTC().Add(lifetime)}, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	create.Flags().Duration("expires-in", 24*time.Hour, "Invitation lifetime")
	list := &cobra.Command{Use: "list", Short: "List invitation metadata", Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		var result json.RawMessage
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodGet, "/admin/invitations", nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	revoke := &cobra.Command{Use: "revoke <id>", Short: "Revoke an unused invitation", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("invitation ID must be positive")
		}
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodDelete, "/admin/invitations/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": id, "revoked": true})
	}}
	cmd.AddCommand(create, list, revoke)
	return cmd
}
