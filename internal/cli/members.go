package cli

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/spf13/cobra"
)

func newMemberCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "member", Short: "Manage project collaboration (owner, deployer, viewer)"}
	cmd.PersistentFlags().String("sub", "", "Subdomain name (or .droply.toml)")
	cmd.PersistentFlags().String("project", "", "Project name (or .droply.toml)")
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List project members as JSON", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		path, err := deploymentAPIPath(cmd)
		if err != nil {
			return err
		}
		var result json.RawMessage
		if err := NewAPIClient(LoadConfig()).doJSON(http.MethodGet, path+"/members", nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	set := &cobra.Command{Use: "set <email>", Args: cobra.ExactArgs(1), Short: "Grant or change a member's role", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		role, _ := cmd.Flags().GetString("role")
		if role != "viewer" && role != "deployer" {
			return errors.New("--role must be viewer or deployer")
		}
		path, err := deploymentAPIPath(cmd)
		if err != nil {
			return err
		}
		var result json.RawMessage
		if err := NewAPIClient(LoadConfig()).doJSON(http.MethodPut, path+"/members", map[string]string{"email": args[0], "role": role}, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
	set.Flags().String("role", "viewer", "viewer (read only) or deployer (publish and read)")
	remove := &cobra.Command{Use: "remove <user-id>", Args: cobra.ExactArgs(1), Short: "Remove collaboration access and invalidate member credentials", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || id < 1 {
			return errors.New("user ID must be positive")
		}
		path, err := deploymentAPIPath(cmd)
		if err != nil {
			return err
		}
		if err := NewAPIClient(LoadConfig()).doJSON(http.MethodDelete, path+"/members/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"user_id": id, "removed": true})
	}}
	cmd.AddCommand(list, set, remove)
	return cmd
}

func newProjectsCmd() *cobra.Command {
	return &cobra.Command{Use: "projects", Args: cobra.NoArgs, Short: "List all projects you can access as JSON", SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		var result json.RawMessage
		if err := NewAPIClient(LoadConfig()).doJSON(http.MethodGet, "/projects", nil, &result); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
}
