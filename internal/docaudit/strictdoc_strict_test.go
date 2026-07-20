//go:build strictdoc

package docaudit

// strictDoc = true under the dedicated docaudit CI job
// (`go test ./internal/docaudit -tags=strictdoc`): the boundary-commit lag
// tripwire fails the run instead of logging a warning.
const strictDoc = true
