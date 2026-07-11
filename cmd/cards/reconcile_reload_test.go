package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/somebox/cards/internal/config"
	"github.com/somebox/cards/internal/hooks"
	"github.com/somebox/cards/internal/httpapi"
)

// TestCreateBoardReloadServiceZeroChurn is the P5c acceptance test: creating a
// board triggers reloadLocked + notifyReload, but with unchanged service decls
// the supervisor must not restart children (zero churn).
func TestCreateBoardReloadServiceZeroChurn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".cards")
	if _, err := initWorkspace(dir); err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	marker := filepath.Join(dir, "svc.pid")
	extJSON := map[string]any{
		"extensions": []map[string]any{{
			"id": "keeper", "kind": "service", "autostart": true,
			"restart_policy": "never",
			"run": []string{"bash", "-c",
				`echo $$ >` + marker + `; while true; do sleep 1; done`},
		}},
	}
	raw, _ := json.MarshalIndent(extJSON, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "definitions", "extensions.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	st, svc, result, err := openWorkspace(dir)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	t.Cleanup(func() { svc.Close(); st.Close() })
	if len(result.Extensions) != 1 || result.Extensions[0].ID != "keeper" {
		t.Fatalf("expected keeper service loaded, got %+v", result.Extensions)
	}

	httpSrv, err := httpapi.New(svc, result.Workspace, result.CardTypes, result.Boards, result.Themes, st)
	if err != nil {
		t.Fatal(err)
	}
	app := newReloadableApp(dir, st, svc, result, httpSrv.Router())

	sup := newExtensionSupervisor(extensionSupervisorOpts{
		getSvc:       app.currentService,
		ws:           result.Workspace,
		exts:         result.Extensions,
		workspaceDir: dir,
		cardsURL:     "http://127.0.0.1:0/v1",
	})
	sup.SetDrainTimeout(500 * time.Millisecond)
	app.setAfterReload(func(exts []config.Extension) {
		sup.Reconcile(exts)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = sup.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("supervisor did not stop")
		}
	})

	ts := httptest.NewServer(app)
	t.Cleanup(ts.Close)

	pid := waitAppServicePID(t, sup, "keeper", 5*time.Second)
	starts := sup.ServiceStarts("keeper")

	typeIDs := make([]string, 0, len(result.CardTypes))
	for id := range result.CardTypes {
		typeIDs = append(typeIDs, id)
	}
	if len(typeIDs) == 0 {
		t.Fatal("workspace has no card types for create-board")
	}
	body, _ := json.Marshal(map[string]any{
		"name": "Zero Churn Board", "columns": []string{"todo", "done"},
		"card_type_ids": typeIDs[:1],
	})
	code, out := appDo(t, "POST", ts.URL+"/v1/boards", string(body))
	if code != 201 {
		t.Fatalf("create board: %d %v", code, out)
	}

	time.Sleep(150 * time.Millisecond)
	if got := sup.ServicePID("keeper"); got != pid {
		t.Fatalf("board-create reload churned service pid: before=%d after=%d", pid, got)
	}
	if got := sup.ServiceStarts("keeper"); got != starts {
		t.Fatalf("board-create reload restarted service: starts before=%d after=%d", starts, got)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("original child %d died after board-create reload: %v", pid, err)
	}
}

func waitAppServicePID(t *testing.T, sup *hooks.Supervisor, id string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid := sup.ServicePID(id); pid > 0 {
			if err := syscall.Kill(pid, 0); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("service %q never started", id)
	return 0
}
