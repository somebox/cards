package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRestartPolicyServiceOK(t *testing.T) {
	for _, policy := range []string{"", RestartOnFailure, RestartAlways, RestartNever} {
		exts := []Extension{{
			ID: "svc", Kind: "service", Run: []string{"true"},
			Autostart: true, RestartPolicy: policy,
		}}
		if err := validateExtensions(exts); err != nil {
			t.Fatalf("restart_policy=%q: %v", policy, err)
		}
	}
}

func TestValidateRestartPolicyRejectedOnHookAndRun(t *testing.T) {
	cases := []Extension{
		{ID: "h", Kind: "hook", On: "status_changed", Run: []string{"true"}, RestartPolicy: RestartOnFailure},
		{ID: "r", Kind: "run", Run: []string{"true"}, RestartPolicy: RestartAlways},
	}
	for _, e := range cases {
		err := validateExtensions([]Extension{e})
		if err == nil {
			t.Fatalf("kind=%s: expected restart_policy rejection, got nil", e.Kind)
		}
		if !strings.Contains(err.Error(), "restart_policy is only valid on kind:service") {
			t.Fatalf("kind=%s: unexpected error: %v", e.Kind, err)
		}
	}
}

func TestValidateRestartPolicyUnknownRejected(t *testing.T) {
	err := validateExtensions([]Extension{{
		ID: "svc", Kind: "service", Run: []string{"true"},
		RestartPolicy: "on_failure", // underscore typo — not a valid value
	}})
	if err == nil {
		t.Fatal("expected unknown restart_policy rejection")
	}
	if !strings.Contains(err.Error(), "unknown restart_policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadExtensionsRestartPolicyJSON(t *testing.T) {
	dir := t.TempDir()
	def := filepath.Join(dir, "definitions")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(def, "extensions.json"), `{
		"extensions": [
			{
				"id": "dropbox",
				"kind": "service",
				"autostart": true,
				"restart_policy": "always",
				"run": ["node", "dropbox.mjs"]
			},
			{
				"id": "notify",
				"kind": "hook",
				"on": "status_changed",
				"run": ["bash", "notify.sh"]
			}
		]
	}`)
	exts, err := LoadExtensions(dir)
	if err != nil {
		t.Fatalf("LoadExtensions: %v", err)
	}
	if len(exts) != 2 {
		t.Fatalf("len=%d, want 2", len(exts))
	}
	if exts[0].RestartPolicy != RestartAlways {
		t.Errorf("service restart_policy=%q, want %q", exts[0].RestartPolicy, RestartAlways)
	}
	if exts[1].RestartPolicy != "" {
		t.Errorf("hook restart_policy=%q, want empty", exts[1].RestartPolicy)
	}
}

func TestLoadExtensionsRestartPolicyOnHookRejected(t *testing.T) {
	dir := t.TempDir()
	def := filepath.Join(dir, "definitions")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(def, "extensions.json"), `{
		"extensions": [{
			"id": "bad",
			"kind": "hook",
			"on": "card_created",
			"restart_policy": "never",
			"run": ["true"]
		}]
	}`)
	_, err := LoadExtensions(dir)
	if err == nil {
		t.Fatal("expected load rejection for restart_policy on hook")
	}
	if !strings.Contains(err.Error(), "restart_policy is only valid on kind:service") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadExtensionsRestartPolicyYAML(t *testing.T) {
	dir := t.TempDir()
	def := filepath.Join(dir, "definitions")
	if err := os.MkdirAll(def, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(def, "extensions.yaml"), `
- id: dropbox
  kind: service
  autostart: true
  restart_policy: on-failure
  run: ["node", "dropbox.mjs"]
`)
	exts, err := LoadExtensions(dir)
	if err != nil {
		t.Fatalf("LoadExtensions: %v", err)
	}
	if len(exts) != 1 || exts[0].RestartPolicy != RestartOnFailure {
		t.Fatalf("got %+v", exts)
	}
}
