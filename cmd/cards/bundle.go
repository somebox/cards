package main

// Artifact bundles — the opt-in --with-artifacts half of export/import
// (card 700b2dfe). A bundle is the JSONL snapshot plus an artifacts/
// directory beside it holding the referenced blobs in the same
// content-addressed layout as a workspace artifacts root (<aa>/<sha256>),
// so a bundle directory is itself a valid artifacts root. Default export
// and import stay pointer-only; the bundle path is for a self-contained
// workspace hand-off.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/somebox/cards/internal/artifacts"
	"github.com/somebox/cards/internal/core"
	"github.com/somebox/cards/internal/sqlite"
)

// artifactRefs returns the {uri, sha256} pointers held by c's
// artifact-typed fields per the type definition — top-level fields and
// repeating item fields. Pointers without both a local relative uri and a
// sha256 are skipped: uri-policy artifacts reference shared storage and do
// not travel in a bundle. A card whose type is absent from the current
// definitions contributes nothing (there is no schema to say which fields
// hold artifacts).
func artifactRefs(c *core.Card, types map[string]*core.CardType) []artifacts.Meta {
	td := types[c.TypeID]
	if td == nil {
		return nil
	}
	fields, ok := c.Fields.(map[string]any)
	if !ok {
		return nil
	}
	var refs []artifacts.Meta
	add := func(v any) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		b, err := json.Marshal(m)
		if err != nil {
			return
		}
		var meta artifacts.Meta
		if json.Unmarshal(b, &meta) != nil {
			return
		}
		if meta.URI == "" || meta.SHA256 == "" {
			return
		}
		if strings.Contains(meta.URI, "://") || path.IsAbs(meta.URI) {
			return // uri-policy / remote — not local bytes
		}
		refs = append(refs, meta)
	}
	for _, f := range td.Fields {
		switch f.Type {
		case core.FieldArtifact:
			add(fields[f.ID])
		case core.FieldRepeating:
			items, _ := fields[f.ID].([]any)
			for _, item := range items {
				im, ok := item.(map[string]any)
				if !ok {
					continue
				}
				for _, itf := range f.ItemFields {
					if itf.Type == core.FieldArtifact {
						add(im[itf.ID])
					}
				}
			}
		}
	}
	return refs
}

// exportArtifacts copies every artifact blob referenced by the workspace's
// cards into destRoot (bundle layout = workspace layout), verifying each
// blob against its pointer's sha256 while copying. A missing or corrupt
// workspace blob fails the export loudly — a bundle that silently drops
// bytes is worse than one that fails.
//
// The copy is idempotent and self-safe: a destination file that already
// holds matching bytes is verified, not rewritten. That also covers the
// dogfooding layout where the snapshot lives inside the workspace dir
// (.cards/backlog.jsonl beside .cards/artifacts/) and destRoot IS the
// workspace artifacts root.
func exportArtifacts(ctx context.Context, st *sqlite.Store, types map[string]*core.CardType, am *artifacts.Manager, destRoot string) (int, error) {
	seen := map[string]bool{}
	count := 0
	cursor := ""
	for {
		page, err := st.ListCards(ctx, core.CardQuery{Limit: 500, Cursor: cursor})
		if err != nil {
			return count, fmt.Errorf("bundle: list cards: %w", err)
		}
		for i := range page.Items {
			c := &page.Items[i]
			for _, ref := range artifactRefs(c, types) {
				if seen[ref.URI] {
					continue
				}
				seen[ref.URI] = true
				if err := copyBlob(am, ref, destRoot); err != nil {
					return count, fmt.Errorf("bundle: card %s: %w", c.ID, err)
				}
				count++
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return count, nil
}

// copyBlob places one referenced blob at destRoot/<uri>, sha256-verified.
func copyBlob(am *artifacts.Manager, ref artifacts.Meta, destRoot string) error {
	rel := filepath.FromSlash(ref.URI)
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("artifact uri %q is not a local relative path", ref.URI)
	}
	dst := filepath.Join(destRoot, rel)

	// Already present (re-export, or destRoot is the workspace root itself):
	// verify in place instead of copying a file onto itself.
	if fi, err := os.Stat(dst); err == nil && fi.Mode().IsRegular() {
		sum, err := fileSHA256(dst)
		if err != nil {
			return err
		}
		if sum != ref.SHA256 {
			return fmt.Errorf("artifact %s: existing bundle bytes hash %s, pointer says %s", ref.URI, sum, ref.SHA256)
		}
		return nil
	}

	src, err := am.Open(ref.URI)
	if err != nil {
		return fmt.Errorf("artifact %s: bytes missing from workspace artifacts root: %w", ref.URI, err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("artifact %s: %w", ref.URI, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".bundle-*")
	if err != nil {
		return fmt.Errorf("artifact %s: %w", ref.URI, err)
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("artifact %s: copy: %w", ref.URI, err)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != ref.SHA256 {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("artifact %s: workspace bytes hash %s, pointer says %s (workspace blob corrupt)", ref.URI, sum, ref.SHA256)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("artifact %s: %w", ref.URI, err)
	}
	return nil
}

// restoreArtifacts reads a bundle's JSONL (as produced by exportJSONL) and
// stages every referenced blob from srcRoot into the workspace artifact
// store, verifying sha256 before commit — a tampered or truncated blob
// fails loudly and is never published. The import command runs this BEFORE
// importJSONL so a bad bundle leaves the DB untouched.
func restoreArtifacts(r io.Reader, types map[string]*core.CardType, srcRoot string, am *artifacts.Manager) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024) // match importJSONL
	seen := map[string]bool{}
	count := 0
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var env portEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return count, fmt.Errorf("line %d: parse: %w", line, err)
		}
		if env.Type != "card" {
			continue
		}
		var c core.Card
		if err := json.Unmarshal(env.Data, &c); err != nil {
			return count, fmt.Errorf("line %d: card: %w", line, err)
		}
		for _, ref := range artifactRefs(&c, types) {
			if seen[ref.URI] {
				continue
			}
			seen[ref.URI] = true
			if err := restoreBlob(am, ref, srcRoot); err != nil {
				return count, fmt.Errorf("card %s: %w", c.ID, err)
			}
			count++
		}
	}
	if err := sc.Err(); err != nil {
		return count, fmt.Errorf("read input: %w", err)
	}
	return count, nil
}

// restoreBlob stages one bundle blob into the artifact store, verifying that
// the bytes hash to the pointer's sha256 before committing.
func restoreBlob(am *artifacts.Manager, ref artifacts.Meta, srcRoot string) error {
	rel := filepath.FromSlash(ref.URI)
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("artifact uri %q is not a local relative path", ref.URI)
	}
	f, err := os.Open(filepath.Join(srcRoot, rel))
	if err != nil {
		return fmt.Errorf("bundle is missing artifact %s: %w", ref.URI, err)
	}
	staged, err := am.Stage(f)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("artifact %s: %w", ref.URI, err)
	}
	if got := staged.Meta().SHA256; got != ref.SHA256 {
		_ = staged.Discard()
		return fmt.Errorf("artifact %s: bundle bytes hash %s, pointer says %s (bundle tampered or corrupt)", ref.URI, got, ref.SHA256)
	}
	if err := staged.Commit(); err != nil {
		return fmt.Errorf("artifact %s: %w", ref.URI, err)
	}
	return nil
}

// fileSHA256 hashes an existing file.
func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
