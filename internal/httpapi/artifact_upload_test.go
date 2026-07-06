package httpapi_test

// Phase 4 (sprint 2026-07-06) — the browser upload path over HTTP: raw-body
// POST with the optional ?version guard, a clean oversize response (not a
// 500), the stale-version 409 the UI renders as "reload", and the board card
// rendering an uploaded artifact. Payloads are generated in-test (never a
// committed fixture) and the server runs on the t.TempDir-backed harness, so
// the whole matrix is `go test ./...`-repeatable.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// rawPostVersion is rawPost with an optional ?version=N guard.
func rawPostVersion(t *testing.T, base, id, field string, version int, body []byte) (*http.Response, []byte) {
	t.Helper()
	url := base + "/v1/cards/" + id + "/artifacts/" + field
	if version != 0 {
		url += fmt.Sprintf("?version=%d", version)
	}
	return rawPost(t, url, body)
}

func TestArtifactUpload_VersionGuardRoundTrip(t *testing.T) {
	ts, id := newArtifactServer(t)

	// Correct version → 201, and the returned card is at version 2.
	resp, data := rawPostVersion(t, ts.URL, id, "screenshot", 1, []byte("upload-with-version"))
	if resp.StatusCode != 201 {
		t.Fatalf("versioned upload: %d %s", resp.StatusCode, data)
	}
	var card struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	if card.Version != 2 {
		t.Errorf("version after upload = %d, want 2", card.Version)
	}
}

func TestArtifactUpload_StaleVersion409(t *testing.T) {
	ts, id := newArtifactServer(t)

	// The card is at version 1; a stale ?version=1 after a first upload (which
	// bumps it to 2) must be rejected with the 409 shape the UI renders as
	// "card changed — reload".
	if resp, data := rawPostVersion(t, ts.URL, id, "screenshot", 1, []byte("first")); resp.StatusCode != 201 {
		t.Fatalf("first upload: %d %s", resp.StatusCode, data)
	}
	resp, data := rawPostVersion(t, ts.URL, id, "screenshot", 1, []byte("second, stale"))
	if resp.StatusCode != 409 {
		t.Fatalf("stale upload: %d %s (want 409)", resp.StatusCode, data)
	}
	var e struct {
		Code string `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	if e.Code != "version_conflict" {
		t.Errorf("stale upload error = %q, want version_conflict", e.Code)
	}
}

func TestArtifactUpload_OversizeIsTypedNot500(t *testing.T) {
	ts, id := newArtifactServer(t)

	// Generate a body over the 32 MiB cap (33 MiB of zeros) — never a committed
	// fixture. MaxBytesReader errors mid-stream; the handler must translate that
	// to a clean 413, not fall through as a generic 500.
	big := bytes.Repeat([]byte{0}, 33<<20)
	resp, data := rawPost(t, ts.URL+"/v1/cards/"+id+"/artifacts/screenshot", big)
	if resp.StatusCode == 500 {
		t.Fatalf("oversize upload returned 500 (unhandled MaxBytesReader): %s", data)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload: %d %s (want 413)", resp.StatusCode, data)
	}
	var e struct {
		Code string `json:"error"`
	}
	_ = json.Unmarshal(data, &e)
	if e.Code != "artifact_too_large" {
		t.Errorf("oversize error = %q, want artifact_too_large", e.Code)
	}
}

// TestBoardCardRendersUploadedArtifact confirms the board card partial shows an
// uploaded image as a thumbnail linking to the confined serve route.
func TestBoardCardRendersUploadedArtifact(t *testing.T) {
	ts, id := newArtifactServer(t)

	// A 1x1 PNG so the server sniffs image/png and the board renders a thumb.
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	if resp, data := rawPost(t, ts.URL+"/v1/cards/"+id+"/artifacts/screenshot", png); resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, data)
	}

	// Fetch the board and assert the card's artifact thumbnail is present.
	resp, err := http.Get(ts.URL + "/ui/boards/eng")
	if err != nil {
		t.Fatalf("get board: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if !strings.Contains(html, "card__artifacts") {
		t.Errorf("board card missing the artifacts row:\n%s", html)
	}
	if !strings.Contains(html, "card__artifact-thumb") {
		t.Errorf("board card missing an image thumbnail for the uploaded PNG")
	}
	if !strings.Contains(html, "/v1/artifacts/") {
		t.Errorf("thumbnail does not point at the confined /v1/artifacts serve route")
	}
}
