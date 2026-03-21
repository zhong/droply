package cli

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var excludeDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".DS_Store":    true,
	".env":         true,
}

// createTarGz walks srcDir and writes a gzipped tar archive to w.
// It excludes hidden directories, node_modules, .git, __pycache__, .DS_Store, and .env.
func createTarGz(w io.Writer, srcDir string) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		name := info.Name()

		// Skip excluded names.
		if excludeDirs[name] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden directories (name starts with '.' and is a directory).
		if info.IsDir() && strings.HasPrefix(name, ".") {
			return filepath.SkipDir
		}

		// Skip hidden files.
		if !info.IsDir() && strings.HasPrefix(name, ".") {
			return nil
		}

		// Compute path relative to srcDir.
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Skip root dir entry itself.
		if rel == "." {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func newDeployCmd() *cobra.Command {
	var sub, project string

	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Deploy a directory to droply",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcDir := "."
			if len(args) == 1 {
				srcDir = args[0]
			}

			// Fall back to .droply.toml for sub and project.
			if sub == "" || project == "" {
				pc, err := LoadProjectConfig()
				if err == nil {
					if sub == "" {
						sub = pc.Subdomain
					}
					if project == "" {
						project = pc.Project
					}
				}
			}
			if sub == "" {
				return fmt.Errorf("subdomain is required: use --sub or set subdomain in .droply.toml")
			}
			if project == "" {
				return fmt.Errorf("project is required: use --project or set project in .droply.toml")
			}

			// Create a temp file for the tar.gz archive.
			tmpFile, err := os.CreateTemp("", "droply-deploy-*.tar.gz")
			if err != nil {
				return fmt.Errorf("create temp file: %w", err)
			}
			defer os.Remove(tmpFile.Name())
			defer tmpFile.Close()

			fmt.Printf("Packaging %s...\n", srcDir)
			if err := createTarGz(tmpFile, srcDir); err != nil {
				return fmt.Errorf("package: %w", err)
			}
			tmpFile.Close()

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/deploy", sub, project)
			fmt.Printf("Uploading to %s/%s...\n", sub, project)
			result, err := client.uploadFile(apiPath, tmpFile.Name())
			if err != nil {
				return err
			}

			version, _ := result["version"].(float64)
			url, _ := result["url"].(string)
			fmt.Printf("Deployed successfully!\n")
			fmt.Printf("  Version: %d\n", int(version))
			fmt.Printf("  URL:     %s\n", url)
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	cmd.Flags().StringVar(&project, "project", "", "Project name")
	return cmd
}
