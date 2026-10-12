package demo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallWritesWorkspace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cards-demo")
	if err := Install(dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, rel := range []string{
		"README.md",
		"definitions/workspace.json",
		"definitions/boards/engineering.json",
		"definitions/boards/welcome.json",
		"definitions/extensions.json",
		".cards/logs/.gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// The extension launchers must be runnable as installed.
	for _, rel := range []string{".cards/ext/notify.sh", ".cards/ext/review-bot.sh", ".cards/ext/review-bot.mjs"} {
		fi, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s is not executable: %v", rel, fi.Mode())
		}
	}
}

// TestInstallRefusesNonEmptyDir — the demo never lands on top of an existing
// workspace or project.
func TestInstallRefusesNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Install(dir)
	if err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("Install into a non-empty dir: err = %v, want a not-empty refusal", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "definitions")); !os.IsNotExist(err) {
		t.Errorf("refused install still wrote definitions/ (stat err = %v)", err)
	}
}

// TestInstallShipsNoMachineLocalState — logs, the live DB and snapshots stay
// out of the binary even if a server once ran against the assets dir.
func TestInstallShipsNoMachineLocalState(t *testing.T) {
	dir := t.TempDir()
	if err := Install(dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		if strings.HasSuffix(name, ".log") || strings.HasPrefix(name, "work-cards.db") || strings.HasSuffix(name, ".jsonl") {
			t.Errorf("demo ships machine-local file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
