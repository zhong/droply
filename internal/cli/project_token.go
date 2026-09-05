package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

func newProjectTokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project-token", Short: "Create, list, and revoke scoped CI credentials"}
	cmd.PersistentFlags().String("sub", "", "Subdomain name (or .droply.toml)")
	cmd.PersistentFlags().String("project", "", "Project name (or .droply.toml)")
	create := &cobra.Command{Use: "create", Short: "Print a new token once as JSON; save it in your CI secret store", Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		path, err := deploymentAPIPath(cmd)
		if err != nil {
			return err
		}
		expiry, _ := cmd.Flags().GetDuration("expires-in")
		if expiry <= 0 || expiry > 365*24*time.Hour {
			return errors.New("--expires-in must be positive and at most 8760h")
		}
		scopes, _ := cmd.Flags().GetStringSlice("scope")
		for _, scope := range scopes {
			if scope != "preview" && scope != "production" {
				return errors.New("--scope must be preview or production")
			}
		}
		name, _ := cmd.Flags().GetString("name")
		input := map[string]any{"name": name, "scopes": scopes, "expires_at": time.Now().UTC().Add(expiry)}
		var result json.RawMessage
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodPost, path+"/tokens", input, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	create.Flags().String("name", "ci", "Credential name")
	create.Flags().StringSlice("scope", []string{"preview"}, "Allowed scope: preview or production (repeat or comma-separate)")
	create.Flags().Duration("expires-in", 30*24*time.Hour, "Lifetime, maximum 8760h")
	list := &cobra.Command{Use: "list", Short: "List credential metadata without secrets", Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		path, err := deploymentAPIPath(cmd)
		if err != nil {
			return err
		}
		var result json.RawMessage
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodGet, path+"/tokens", nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	revoke := &cobra.Command{Use: "revoke <id>", Short: "Revoke a project credential", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("token ID must be positive")
		}
		path, err := deploymentAPIPath(cmd)
		if err != nil {
			return err
		}
		cfg, err := LoadConfig()
		if err != nil {
			return err
		}
		if err := NewAPIClient(cfg).doJSONContext(cmd.Context(), http.MethodDelete, path+"/tokens/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": id, "revoked": true})
	}}
	cmd.AddCommand(create, list, revoke)
	return cmd
}
