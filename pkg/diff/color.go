package diff

import (
	"fmt"
	"io"
	"strings"

	"github.com/mgutz/ansi"
	"github.com/pmezard/go-difflib/difflib"
)

// colorDiffer produces a unified diff, optionally with ANSI colors.
type colorDiffer struct {
	context int
	noColor bool
}

func (d *colorDiffer) Diff(w io.Writer, header string, old, new string) error {
	oldLines := splitLines(old)
	newLines := splitLines(new)

	groups := difflib.NewMatcher(oldLines, newLines).GetGroupedOpCodes(d.context)
	if len(groups) == 0 {
		return nil
	}

	for _, group := range groups {
		first := group[0]
		last := group[len(group)-1]
		rangeStr := fmt.Sprintf("@@ -%d,%d +%d,%d @@",
			first.I1+1, last.I2-first.I1,
			first.J1+1, last.J2-first.J1)
		fmt.Fprintln(w, d.style(rangeStr, "cyan")) //nolint:errcheck

		for _, op := range group {
			switch op.Tag {
			case 'e': // equal
				for _, line := range oldLines[op.I1:op.I2] {
					fmt.Fprintln(w, "  "+line) //nolint:errcheck
				}
			case 'r': // replace
				for _, line := range oldLines[op.I1:op.I2] {
					fmt.Fprintln(w, d.style("- "+line, "red")) //nolint:errcheck
				}
				for _, line := range newLines[op.J1:op.J2] {
					fmt.Fprintln(w, d.style("+ "+line, "green")) //nolint:errcheck
				}
			case 'd': // delete
				for _, line := range oldLines[op.I1:op.I2] {
					fmt.Fprintln(w, d.style("- "+line, "red")) //nolint:errcheck
				}
			case 'i': // insert
				for _, line := range newLines[op.J1:op.J2] {
					fmt.Fprintln(w, d.style("+ "+line, "green")) //nolint:errcheck
				}
			}
		}
	}

	return nil
}

func (d *colorDiffer) style(text, color string) string {
	if d.noColor {
		return text
	}
	return ansi.Color(text, color)
}


func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	// Remove trailing empty line from trailing newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
