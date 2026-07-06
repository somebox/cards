package main

// Resolver tests for the ONE explicit-path rule (sprint 2026-07-06 Phase 2):
// --workspace/$CARDS_WORKSPACE accept the workspace dir itself or a project
// root whose .cards child is the workspace; the both-valid layout errors
// with the concrete choices instead of guessing. Every case builds its
// directory layout in t.TempDir() via the mkWorkspace fixture, so setup and
// teardown are automatic and cases are trivially extendable.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkWorkspace makes dir look like a workspace: definitions/workspace.json
// exists (content is irrelevant to resolution — the resolver only stats it).
func mkWorkspace(t *testing.T, dir string) {
	t.Helper()
	defs := filepath.Join(dir, "definitions")
	if err := os.MkdirAll(defs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defs, "workspace.json"), []byte(`{"id":"t","name":"T"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveWorkspaceDir_ExplicitPathRule(t *testing.T) {
	t.Run("workspace dir itself resolves verbatim", func(t *testing.T) {
		root := t.TempDir()
		mkWorkspace(t, root)
		got, autoInit, err := resolveWorkspaceDir(root)
		if err != nil || autoInit {
			t.Fatalf("resolve: %v autoInit=%v", err, autoInit)
		}
		if got != root {
			t.Errorf("got %s, want %s", got, root)
		}
	})

	t.Run("project root resolves to its .cards child", func(t *testing.T) {
		root := t.TempDir()
		mkWorkspace(t, filepath.Join(root, ".cards"))
		got, _, err := resolveWorkspaceDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(root, ".cards"); got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})

	t.Run("both valid fails with the concrete choices", func(t *testing.T) {
		root := t.TempDir()
		mkWorkspace(t, root)
		mkWorkspace(t, filepath.Join(root, ".cards"))
		_, _, err := resolveWorkspaceDir(root)
		if err == nil {
			t.Fatal("expected the ambiguous-workspace error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, filepath.Join(root, ".cards")) || !strings.Contains(msg, "ambiguous") {
			t.Errorf("error should name both candidates and say ambiguous:\n%s", msg)
		}
	})

	t.Run("neither passes through verbatim (open fails loudly later)", func(t *testing.T) {
		root := t.TempDir()
		got, _, err := resolveWorkspaceDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if got != root {
			t.Errorf("got %s, want %s", got, root)
		}
	})

	t.Run("relative path is absolutized", func(t *testing.T) {
		root := t.TempDir()
		mkWorkspace(t, filepath.Join(root, ".cards"))
		t.Chdir(root)
		got, _, err := resolveWorkspaceDir(".")
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(got) || filepath.Base(got) != ".cards" {
			t.Errorf("got %s, want absolute path to .cards child", got)
		}
	})
}

func TestInitCmd_RefusesExistingWorkspaceTarget(t *testing.T) {
	// `cards init X` where X is ALREADY a workspace dir would scaffold
	// X/.cards inside it — the exact nested layout normalizeWorkspaceDir
	// refuses as ambiguous. init must refuse to build the trap.
	root := t.TempDir()
	mkWorkspace(t, root)
	err := initCmd([]string{"--quiet", root})
	if err == nil {
		t.Fatal("expected init to refuse an existing workspace target")
	}
	if !strings.Contains(err.Error(), "already a workspace") {
		t.Errorf("error should say why: %v", err)
	}
}
