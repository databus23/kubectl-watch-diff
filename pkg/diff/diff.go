package diff

import (
	"fmt"
	"io"
)

// Differ is the interface for computing and printing diffs between two YAML strings.
type Differ interface {
	// Diff computes and writes the diff between old and new YAML content.
	// The header provides context (resource name, namespace, etc.).
	Diff(w io.Writer, header string, old, new string) error
}

// New creates a Differ for the given output format.
func New(format string, contextLines int, noColor bool) (Differ, error) {
	switch format {
	case "diff", "":
		return &colorDiffer{context: contextLines, noColor: noColor}, nil
	case "dyff":
		return &dyffDiffer{}, nil
	default:
		return nil, fmt.Errorf("unknown output format %q, supported: diff, dyff", format)
	}
}
