//go:build !cgo

package codegraph

// newTreeSitterParser reports that tree-sitter is unavailable when the build
// has no cgo toolchain. NewParser then returns the pure-Go fallback parser.
func newTreeSitterParser() (Parser, bool) {
	return nil, false
}
