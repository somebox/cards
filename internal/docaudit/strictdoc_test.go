//go:build !strictdoc

package docaudit

// strictDoc gates the boundary-commit tripwire
// (TestImplStatusBoundaryCommit). In normal `go test ./...` runs the
// within-N lag check is a warning, so an unrelated PR never goes red purely
// because boundaryMaxLag commits accumulated (which would train contributors
// to ignore docaudit failures). The dedicated docaudit CI job builds with
// -tags=strictdoc (see strictdoc_strict_test.go) to make the check a failure.
// The anchor and doc-path tests are strict everywhere, tag or not.
const strictDoc = false
