// Package artifacts manages workspace on-disk artifact bytes referenced by
// artifact fields. Cards hold metadata only ({uri, mime, size, sha256}).
//
// Local artifact URIs must resolve under the workspace artifacts root
// (artifact_policy: "local"). See docs/SPEC.md §6 and docs/ARCHITECTURE.md
// (Security and Trust Boundary).
package artifacts

// Manager stores and resolves artifact bytes.
type Manager struct {
	// TODO: content-addressed or per-card subdirs, sha256, mime sniff,
	// path-confinement validation for local policy.
}
