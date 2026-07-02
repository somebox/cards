package artifacts

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newManager(t *testing.T) (*Manager, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "artifacts")
	m, err := New(root)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return m, root
}

func TestPutStoresContentAddressed(t *testing.T) {
	m, root := newManager(t)
	content := []byte("hello artifacts")
	meta, err := m.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if meta.Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", meta.Size, len(content))
	}
	if len(meta.SHA256) != 64 || !strings.HasPrefix(meta.URI, meta.SHA256[:2]+"/") {
		t.Errorf("uri %q not content-addressed by sha %q", meta.URI, meta.SHA256)
	}
	if !strings.HasPrefix(meta.MIME, "text/plain") {
		t.Errorf("mime = %q, want text/plain*", meta.MIME)
	}
	// Bytes land where the URI says, under the root.
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(meta.URI)))
	if err != nil || !bytes.Equal(b, content) {
		t.Errorf("stored bytes = %q, %v", b, err)
	}
}

func TestPutDeduplicatesAndSniffsBinary(t *testing.T) {
	m, root := newManager(t)
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	m1, err := m.Put(bytes.NewReader(png))
	if err != nil {
		t.Fatalf("put 1: %v", err)
	}
	m2, err := m.Put(bytes.NewReader(png))
	if err != nil {
		t.Fatalf("put 2: %v", err)
	}
	if m1.URI != m2.URI || m1.SHA256 != m2.SHA256 {
		t.Errorf("identical content produced different URIs: %q vs %q", m1.URI, m2.URI)
	}
	if m1.MIME != "image/png" {
		t.Errorf("mime = %q, want image/png", m1.MIME)
	}
	// No stray ingest temp files left behind.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ingest-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestOpenRoundTrip(t *testing.T) {
	m, _ := newManager(t)
	meta, err := m.Put(strings.NewReader("round trip"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := m.Open(meta.URI)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "round trip" {
		t.Errorf("read %q", b)
	}
}

// The point of the package: traversal attempts must fail before any endpoint
// ever calls into it (DEBT-57).
func TestResolveRejectsEscapes(t *testing.T) {
	m, root := newManager(t)
	meta, err := m.Put(strings.NewReader("legit"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	// A secret outside the root that attackers aim for.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"",
		"../secret.txt",
		"a/../../secret.txt",
		meta.URI + "/../../../secret.txt",
		"/etc/passwd",
		outside, // absolute path to the planted secret
		"..",
	}
	for _, uri := range bad {
		if p, err := m.Resolve(uri); err == nil {
			t.Errorf("Resolve(%q) = %q, want error", uri, p)
		}
	}

	// Legit URI still resolves.
	if _, err := m.Resolve(meta.URI); err != nil {
		t.Errorf("Resolve(%q): %v", meta.URI, err)
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	m, root := newManager(t)
	if _, err := m.Put(strings.NewReader("x")); err != nil { // ensures root exists
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if p, err := m.Resolve("leak"); err == nil {
		t.Errorf("Resolve(leak symlink) = %q, want error", p)
	}
	// A symlinked directory escape, too.
	dirLink := filepath.Join(root, "leakdir")
	if err := os.Symlink(filepath.Dir(root), dirLink); err == nil {
		if p, err := m.Resolve("leakdir/secret.txt"); err == nil {
			t.Errorf("Resolve(leakdir/secret.txt) = %q, want error", p)
		}
	}
}

func TestResolveRequiresExistingArtifact(t *testing.T) {
	m, _ := newManager(t)
	if _, err := m.Put(strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Resolve("ab/doesnotexist"); err == nil {
		t.Error("Resolve of missing artifact succeeded")
	}
}
