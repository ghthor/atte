package attehcl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ghthor/atte/detector/attegit"
	"github.com/ghthor/atte/reference"
	"github.com/hashicorp/hcl/v2"
)

func hclDiagnosticError(repo *attegit.Repo, file reference.Blob, diags hcl.Diagnostics) error {
	if len(diags) == 0 {
		return fmt.Errorf("decode HCL %q: no diagnostics", file)
	}

	parts := make([]string, 0, len(diags))
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		location := file.String()
		context := ""
		if diag.Subject != nil {
			location = diag.Subject.String()
			context = hclDiagnosticContext(repo, file, *diag.Subject)
		}
		message := diag.Summary
		if diag.Detail != "" {
			message += ": " + diag.Detail
		}
		if context != "" {
			message = context + "\n" + message
		}
		parts = append(parts, fmt.Sprintf("decode HCL %q: %s:\n%s", file, location, message))
	}
	if len(parts) == 0 {
		return fmt.Errorf("decode HCL %q: no diagnostics", file)
	}
	return errors.New(strings.Join(parts, "\n"))
}

func hclDiagnosticContext(repo *attegit.Repo, file reference.Blob, subject hcl.Range) string {
	if repo == nil || subject.Start.Line < 1 {
		return ""
	}
	contents, err := repo.Show(file)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(contents), "\n")
	lineIndex := subject.Start.Line - 1
	if lineIndex >= len(lines) {
		return ""
	}
	line := lines[lineIndex]
	start := max(subject.Start.Column-1, 0)
	end := subject.End.Column - 1
	if end <= start {
		end = start + 1
	}
	marker := strings.Repeat(" ", start) + strings.Repeat("^", end-start)
	return fmt.Sprintf("  %d | %s\n    | %s", subject.Start.Line, line, marker)
}
