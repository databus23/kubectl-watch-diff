package diff

import (
	"io"
	"os"
	"strings"

	"github.com/gonvenience/ytbx"
	"github.com/homeport/dyff/pkg/dyff"
)

// dyffDiffer produces structural diff output using the dyff library.
type dyffDiffer struct{}

func (d *dyffDiffer) Diff(w io.Writer, header string, old, new string) error {
	oldFile, err := os.CreateTemp("", "diff-watch-old-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(oldFile.Name()) //nolint:errcheck

	newFile, err := os.CreateTemp("", "diff-watch-new-*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(newFile.Name()) //nolint:errcheck

	if _, err := oldFile.WriteString(ensureTrailingNewline(old)); err != nil {
		return err
	}
	if err := oldFile.Close(); err != nil {
		return err
	}

	if _, err := newFile.WriteString(ensureTrailingNewline(new)); err != nil {
		return err
	}
	if err := newFile.Close(); err != nil {
		return err
	}

	from, to, err := ytbx.LoadFiles(oldFile.Name(), newFile.Name())
	if err != nil {
		return err
	}

	report, err := dyff.CompareInputFiles(from, to,
		dyff.IgnoreWhitespaceChanges(true),
		dyff.KubernetesEntityDetection(true),
	)
	if err != nil {
		return err
	}

	if len(report.Diffs) == 0 {
		return nil
	}

	reportWriter := &dyff.HumanReport{
		Report:     report,
		OmitHeader: true,
	}

	return reportWriter.WriteReport(w)
}

func ensureTrailingNewline(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}
