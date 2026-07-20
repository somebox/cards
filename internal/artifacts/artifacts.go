// Package artifacts manages workspace on-disk artifact bytes referenced by
// artifact fields. Cards hold metadata only ({uri, mime, size, sha256}).
//
// Storage is content-addressed: bytes live at <root>/<sha256[:2]>/<sha256>,
// so identical content stores once and a URI can never be overwritten with
// different bytes. Local artifact URIs are relative and must resolve under
// the workspace artifacts root (artifact_policy: "local") — Resolve enforces
// that boundary, including symlink escapes. See docs/spec/card-types.md
// (`artifact` field) and docs/architecture/index.md (Security and Trust
// Boundary).
//
// This package is wired through every surface (HTTP POST
// /v1/cards/{id}/artifacts/{field} + GET /v1/artifacts/*, the CLI `attach`
// verb, and MCP attach_artifact). The confinement policy (Resolve) was
// implemented and adversarially tested before any endpoint shipped, so a
// surface can never outrun the boundary rules.
package artifacts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Meta is the artifact-field metadata a card stores (SPEC §6).
type Meta struct {
	URI    string `json:"uri"`
	MIME   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Manager stores and resolves artifact bytes under one workspace's
// artifacts root.
type Manager struct {
	root string
}

// New binds a Manager to the artifacts root (absolute). The directory is
// created lazily on first Put.
func New(root string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("artifacts: resolve root: %w", err)
	}
	return &Manager{root: abs}, nil
}

// Put ingests content and stores it content-addressed in one step. It is
// Stage followed by Commit — convenient when there is no surrounding
// transaction to coordinate with.
func (m *Manager) Put(r io.Reader) (Meta, error) {
	s, err := m.Stage(r)
	if err != nil {
		return Meta{}, err
	}
	if err := s.Commit(); err != nil {
		_ = s.Discard()
		return Meta{}, err
	}
	return s.Meta(), nil
}

// Staged is content ingested to a temp file with its metadata already known,
// but not yet placed in the content-addressed store. It lets a caller learn
// the URI/sha (to write into a card) and only make the bytes durable once the
// card write commits — so a failed or raced write never orphans a blob.
// Exactly one of Commit or Discard should be called; Discard after Commit is
// a safe no-op.
type Staged struct {
	m    *Manager
	tmp  string // temp file path; "" once consumed by Commit/Discard
	meta Meta
}

// Stage hashes content (SHA-256), sniffs its MIME type from the first 512
// bytes, and writes it to a temp file — computing the final Meta without yet
// publishing the bytes to the store. Call Commit to publish, or Discard to
// drop them.
func (m *Manager) Stage(r io.Reader) (*Staged, error) {
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return nil, fmt.Errorf("artifacts: create root: %w", err)
	}
	tmp, err := os.CreateTemp(m.root, ".ingest-*")
	if err != nil {
		return nil, fmt.Errorf("artifacts: temp file: %w", err)
	}
	tmpName := tmp.Name()

	h := sha256.New()
	var head headWriter
	size, err := io.Copy(io.MultiWriter(tmp, h, &head), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return nil, fmt.Errorf("artifacts: ingest: %w", err)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	return &Staged{
		m:   m,
		tmp: tmpName,
		meta: Meta{
			URI:    path.Join(sum[:2], sum), // relative to the artifacts root
			MIME:   http.DetectContentType(head.buf),
			Size:   size,
			SHA256: sum,
		},
	}, nil
}

// Meta returns the artifact metadata, known as soon as the content is staged.
func (s *Staged) Meta() Meta { return s.meta }

// Commit publishes the staged bytes into the content-addressed store. If
// identical content is already stored it deduplicates (dropping the temp).
// Idempotent: a second call after success is a no-op.
func (s *Staged) Commit() error {
	if s.tmp == "" {
		return nil
	}
	sum := s.meta.SHA256
	dir := filepath.Join(s.m.root, sum[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artifacts: create shard dir: %w", err)
	}
	final := filepath.Join(dir, sum)
	if _, err := os.Stat(final); err == nil {
		// Content already stored; drop the temp duplicate.
		_ = os.Remove(s.tmp)
		s.tmp = ""
		return nil
	}
	if err := os.Rename(s.tmp, final); err != nil {
		return fmt.Errorf("artifacts: store: %w", err)
	}
	s.tmp = ""
	return nil
}

// Discard removes the staged temp file. Call it when the surrounding write
// fails so nothing is orphaned. No-op after Commit.
func (s *Staged) Discard() error {
	if s.tmp == "" {
		return nil
	}
	err := os.Remove(s.tmp)
	s.tmp = ""
	return err
}

// Resolve maps a local artifact URI to an absolute path, enforcing the
// local-policy boundary: the URI must be relative, must not climb out of the
// root (..), and must not escape via symlinks. The artifact must exist.
func (m *Manager) Resolve(uri string) (string, error) {
	if uri == "" {
		return "", fmt.Errorf("artifacts: empty uri")
	}
	if path.IsAbs(uri) || filepath.IsAbs(uri) {
		return "", fmt.Errorf("artifacts: uri %q must be relative to the artifacts root", uri)
	}
	full := filepath.Join(m.root, filepath.FromSlash(uri))
	if !within(m.root, full) {
		return "", fmt.Errorf("artifacts: uri %q escapes the artifacts root", uri)
	}
	// Re-check after resolving symlinks: a link inside the root may point
	// anywhere, and the confined path is the resolved one.
	resolvedRoot, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return "", fmt.Errorf("artifacts: resolve root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("artifacts: resolve %q: %w", uri, err)
	}
	if !within(resolvedRoot, resolved) {
		return "", fmt.Errorf("artifacts: uri %q resolves outside the artifacts root", uri)
	}
	return resolved, nil
}

// Open returns a reader for a local artifact URI, applying Resolve's
// confinement rules.
func (m *Manager) Open(uri string) (io.ReadCloser, error) {
	p, err := m.Resolve(uri)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// within reports whether p is root itself or inside it (both pre-cleaned
// absolute paths).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// headWriter captures the first 512 bytes for MIME sniffing.
type headWriter struct{ buf []byte }

func (h *headWriter) Write(p []byte) (int, error) {
	if len(h.buf) < 512 {
		n := min(512-len(h.buf), len(p))
		h.buf = append(h.buf, p[:n]...)
	}
	return len(p), nil
}
