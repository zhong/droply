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

// formatSize returns a human-readable file size string.
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
	)
	switch {
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

var excludeDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"__pycache__":  true,
	".DS_Store":    true,
	".env":         true,
}

type deployExclusions struct {
	paths map[string]struct{}
	files map[string]struct{}
}

func newDeployExclusions(pc *ProjectConfig) *deployExclusions {
	exclusions := &deployExclusions{
		paths: make(map[string]struct{}),
		files: make(map[string]struct{}),
	}
	if pc == nil {
		return exclusions
	}

	for _, path := range pc.ExcludePaths {
		normalized := normalizeExcludePath(path)
		if normalized == "" {
			continue
		}
		exclusions.paths[normalized] = struct{}{}
	}
	for _, file := range pc.ExcludeFiles {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		exclusions.files[file] = struct{}{}
	}
	return exclusions
}

func normalizeExcludePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(filepath.FromSlash(path))
}

// createTarGz walks srcDir and writes a gzipped tar archive to w.
// It excludes hidden directories, node_modules, .git, __pycache__, .DS_Store, and .env.
// Returns the number of files packaged.
func createTarGz(w io.Writer, srcDir, projectRoot string, pc *ProjectConfig) (int, error) {
	absSrcDir, err := filepath.Abs(srcDir)
	if err != nil {
		return 0, fmt.Errorf("resolve source dir: %w", err)
	}

	absProjectRoot := absSrcDir
	if projectRoot != "" {
		absProjectRoot, err = filepath.Abs(projectRoot)
		if err != nil {
			return 0, fmt.Errorf("resolve project root: %w", err)
		}
	}

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	exclusions := newDeployExclusions(pc)

	var fileCount int
	err = filepath.Walk(absSrcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Compute path relative to srcDir first, so root is handled
		// before any name-based checks.
		rel, err := filepath.Rel(absSrcDir, path)
		if err != nil {
			return err
		}

		name := info.Name()
		projectRel, err := filepath.Rel(absProjectRoot, path)
		if err != nil {
			return err
		}
		projectRel = filepath.Clean(projectRel)

		if _, ok := exclusions.paths[projectRel]; ok {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.IsDir() {
			if _, ok := exclusions.files[name]; ok {
				return nil
			}
		}

		// Skip root dir entry itself.
		if rel == "." {
			return nil
		}

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
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		fileCount++
		return nil
	})
	if err != nil {
		tw.Close()
		gz.Close()
		return fileCount, err
	}

	// Explicitly close in correct order: tar first, then gzip.
	if err := tw.Close(); err != nil {
		gz.Close()
		return fileCount, fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fileCount, fmt.Errorf("gzip close: %w", err)
	}
	return fileCount, nil
}

func newDeployCmd() *cobra.Command {
	var sub, project string

	cmd := &cobra.Command{
		Use:   "deploy [dir]",
		Short: "Deploy a directory to droply",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srcDir := "."
			pc, err := loadOptionalProjectConfig()
			if err != nil {
				return err
			}
			if len(args) == 1 {
				srcDir = args[0]
			}

			// Fall back to .droply.toml for sub and project.
			if pc != nil {
				if sub == "" {
					sub = pc.Subdomain
				}
				if project == "" {
					project = pc.Project
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
			projectRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}

			packaged, err := createTarGz(tmpFile, srcDir, projectRoot, pc)
			if err != nil {
				return fmt.Errorf("package: %w", err)
			}
			tmpFile.Close()
			if packaged == 0 {
				return fmt.Errorf("no files to deploy in %s", srcDir)
			}

			cfg := LoadConfig()
			client := NewAPIClient(cfg)

			apiPath := fmt.Sprintf("/subdomains/%s/projects/%s/deploy", sub, project)
			fmt.Printf("Uploading to %s/%s...\n", sub, project)
			result, err := client.uploadFile(apiPath, tmpFile.Name())
			if err != nil {
				return err
			}

			version, _ := result["version"].(float64)
			fileCount, _ := result["file_count"].(float64)
			totalSize, _ := result["total_size"].(float64)
			url, _ := result["url"].(string)
			fmt.Printf("Deployed successfully!\n")
			fmt.Printf("  Version:    %d\n", int(version))
			fmt.Printf("  Files:      %d\n", int(fileCount))
			fmt.Printf("  Total size: %s\n", formatSize(int64(totalSize)))
			fmt.Printf("  URL:        %s\n", url)
			return nil
		},
	}

	cmd.Flags().StringVar(&sub, "sub", "", "Subdomain name")
	cmd.Flags().StringVar(&project, "project", "", "Project name")
	return cmd
}
