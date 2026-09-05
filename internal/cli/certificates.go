package cli

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
)

func newCertificateCmd() *cobra.Command {
	return &cobra.Command{
		Use: "certificate <domain>", Short: "Show certificate status for a domain you own", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := LoadConfig()
			if err != nil {
				return err
			}
			var status map[string]any
			if err := NewAPIClient(ctx).doJSONContext(cmd.Context(), http.MethodGet, "/certificates/"+url.PathEscape(args[0]), nil, &status); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(status)
		},
	}
}
