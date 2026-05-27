// `agentctl init <template>` scaffolds a new agent directory from a built-in
// template. v1 ships python-llm as the only template path; we copy the
// files verbatim from /templates/<template>/ at the user's pwd.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	var (
		dest     string
		templates string
	)
	c := &cobra.Command{
		Use:   "init <template>",
		Short: "Scaffold a new agent from a template (e.g. python-llm)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tmpl := args[0]
			if templates == "" {
				if v := os.Getenv("AILAB_TEMPLATES_DIR"); v != "" {
					templates = v
				} else {
					templates = "templates"
				}
			}
			src := filepath.Join(templates, tmpl)
			if _, err := os.Stat(src); err != nil {
				return fmt.Errorf("template %q not found at %s; set --templates-dir or AILAB_TEMPLATES_DIR", tmpl, src)
			}
			if dest == "" {
				dest = tmpl
			}
			if _, err := os.Stat(dest); err == nil {
				return fmt.Errorf("destination %s already exists", dest)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := copyTree(src, dest); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "scaffolded %s -> %s\n", tmpl, dest)
			return nil
		},
	}
	c.Flags().StringVar(&dest, "dest", "", "destination directory (defaults to <template>)")
	c.Flags().StringVar(&templates, "templates-dir", "", "directory containing template subdirs (default: ./templates or AILAB_TEMPLATES_DIR)")
	return c
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
