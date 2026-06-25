// Package config loads, merges, and validates workspace definitions from
// JSON/YAML files in definitions/. Reload-on-change is handled in serve mode.
//
// See docs/ARCHITECTURE.md (Config Contexts) and docs/DEVELOPER-REFERENCE.md.
package config

// Loader reads definitions from one or more context paths and merges them
// into a single normalized workspace. See ARCHITECTURE.md (Config Contexts).
type Loader struct {
	// TODO: file watching, JSON+YAML normalization, deterministic merge,
	// --allow-override handling.
}
