// Package demo carries the sample workspace `cards demo` installs: an
// engineering board and a welcome board, seven card types, the jeeruh theme,
// and two example extensions (a review-notify hook and a review-bot service).
// It ships inside the binary rather than as a directory in the repo, so a
// user can get it on demand and the project's own tree has one workspace
// (.cards). Tests install it into a temp dir via internal/demo/demotest.
package demo

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// assets holds the demo workspace tree. The paths are listed rather than
// embedded with all:, so machine-local files a server writes into a workspace
// (logs, the live DB) can never be compiled in by accident.
//
//go:embed assets/README.md assets/definitions assets/.cards/ext assets/.cards/logs/.gitignore
var assets embed.FS

// Install writes the demo workspace into dir, creating it if needed. It
// refuses a dir that already has entries, so it never clobbers a workspace or
// mixes the demo into an unrelated directory. The extension launchers under
// .cards/ext are written executable.
func Install(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case err == nil && len(entries) > 0:
		return fmt.Errorf("%s is not empty — choose a new directory for the demo workspace", dir)
	case err != nil && !os.IsNotExist(err):
		return err
	}
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	return fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(sub, path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(path, ".cards/ext/") {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}
