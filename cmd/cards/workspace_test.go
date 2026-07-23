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

	"github.com/somebox/cards/internal/core"
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

// TestResolveWorkspaceDir_DiscoveryAndGlobal covers the zero-config path the
// card b86c7fe9 acceptance names: walk up to the nearest .cards/, else fall
// back to $CARDS_HOME / ~/.cards with autoInit so the caller can scaffold it.
func TestResolveWorkspaceDir_DiscoveryAndGlobal(t *testing.T) {
	t.Run("walks up from a nested cwd to the nearest .cards", func(t *testing.T) {
		root := t.TempDir()
		ws := filepath.Join(root, ".cards")
		mkWorkspace(t, ws)
		deep := filepath.Join(root, "a", "b", "c")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(deep)

		got, autoInit, err := resolveWorkspaceDir("")
		if err != nil {
			t.Fatal(err)
		}
		if autoInit {
			t.Fatal("discovered workspace must not auto-init")
		}
		if got != ws {
			t.Errorf("got %s, want %s", got, ws)
		}
	})

	t.Run("prefers the nearest .cards over a parent one", func(t *testing.T) {
		root := t.TempDir()
		outer := filepath.Join(root, ".cards")
		innerRoot := filepath.Join(root, "proj")
		inner := filepath.Join(innerRoot, ".cards")
		mkWorkspace(t, outer)
		mkWorkspace(t, inner)
		t.Chdir(innerRoot)

		got, autoInit, err := resolveWorkspaceDir("")
		if err != nil || autoInit {
			t.Fatalf("resolve: %v autoInit=%v", err, autoInit)
		}
		if got != inner {
			t.Errorf("got %s, want nearest %s", got, inner)
		}
	})

	t.Run("CARDS_HOME wins the global fallback and sets autoInit", func(t *testing.T) {
		// Resolve from a temp cwd that has no .cards ancestor so discovery
		// fails and the global path is taken. Isolate HOME too so a real
		// ~/.cards on the developer machine cannot leak into the assert.
		empty := t.TempDir()
		t.Chdir(empty)
		// Climb past the system temp root is impossible; empty has no .cards.
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CARDS_HOME", filepath.Join(home, "my-cards"))

		got, autoInit, err := resolveWorkspaceDir("")
		if err != nil {
			t.Fatal(err)
		}
		if !autoInit {
			t.Fatal("global fallback must signal autoInit")
		}
		want, _ := filepath.Abs(filepath.Join(home, "my-cards"))
		if got != want {
			t.Errorf("got %s, want CARDS_HOME %s", got, want)
		}
	})

	t.Run("without CARDS_HOME falls back to $HOME/.cards", func(t *testing.T) {
		empty := t.TempDir()
		t.Chdir(empty)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CARDS_HOME", "")

		got, autoInit, err := resolveWorkspaceDir("")
		if err != nil {
			t.Fatal(err)
		}
		if !autoInit {
			t.Fatal("expected autoInit on personal fallback")
		}
		want := filepath.Join(home, ".cards")
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
}

func TestInitCmd_LocalAndGlobal(t *testing.T) {
	t.Run("scaffolds ./.cards under the target dir", func(t *testing.T) {
		root := t.TempDir()
		if err := initCmd([]string{"--quiet", root}); err != nil {
			t.Fatalf("init: %v", err)
		}
		ws := filepath.Join(root, ".cards")
		if !isWorkspaceDir(ws) {
			t.Fatalf("expected workspace at %s", ws)
		}
		// Second init is a quiet no-op, never clobbers.
		if err := initCmd([]string{"--quiet", root}); err != nil {
			t.Fatalf("re-init: %v", err)
		}
	})

	t.Run("--global scaffolds CARDS_HOME", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CARDS_HOME", home)
		if err := initCmd([]string{"--quiet", "--global"}); err != nil {
			t.Fatalf("init --global: %v", err)
		}
		if !isWorkspaceDir(home) {
			t.Fatalf("expected personal workspace at CARDS_HOME %s", home)
		}
	})
}

// TestRun_InitQuietGlobal peels `--quiet` as a leading global and reinjects it
// so `cards --quiet init <dir>` truly stays silent (previously --quiet was
// consumed by peelGlobals and init always printed the Next: blurb).
func TestRun_InitQuietGlobal(t *testing.T) {
	root := t.TempDir()
	// Capture stdout: a quiet init must produce no product output.
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = run([]string{"--quiet", "init", root})
	_ = w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	out := make([]byte, 512)
	n, _ := r.Read(out)
	_ = r.Close()
	if n != 0 {
		t.Fatalf("quiet init wrote to stdout: %q", out[:n])
	}
	if !isWorkspaceDir(filepath.Join(root, ".cards")) {
		t.Fatal("quiet init did not scaffold")
	}
}

func TestInitWorkspace_SeedsWelcomeBoard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".cards")
	created, err := initWorkspace(dir)
	if err != nil || !created {
		t.Fatalf("initWorkspace: created=%v err=%v", created, err)
	}
	st, svc, result, err := openWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer svc.Close()

	if _, ok := result.Boards["welcome"]; !ok {
		t.Fatal("starter missing welcome board")
	}
	if _, ok := result.CardTypes["task"]; !ok {
		t.Fatal("starter missing task type")
	}
	page, err := st.ListCards(t.Context(), core.CardQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 {
		t.Fatal("welcome board seeded no onboarding cards")
	}
	// Idempotent: a second init leaves the seed alone.
	if created, err := initWorkspace(dir); err != nil || created {
		t.Fatalf("re-init = (%v, %v), want (false, nil)", created, err)
	}
}
