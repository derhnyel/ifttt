package out

import (
	"fmt"

	core "github.com/derhnyel/ifttt/internal"
)

type Text struct{}

func (Text) Write(fs []core.Finding, suppressed []core.Finding) error {
	for _, f := range fs {
		printFinding(fmt.Sprintf("[%s]", f.RuleID), f)
	}
	for _, f := range suppressed {
		printFinding(fmt.Sprintf("[suppressed:%s]", f.RuleID), f)
	}
	return nil
}

func printFinding(prefix string, f core.Finding) {
	if f.Repository != "" {
		prefix += fmt.Sprintf(" [%s %s..%s]", f.Repository, f.BaseRevision, f.HeadRevision)
	}
	fmt.Printf("%s %s:%d %s\n", prefix, f.File, f.Line, f.Message)
	if f.Summary != "" {
		fmt.Printf("    Summary: %s\n", f.Summary)
	}
	if f.Resolution != "" {
		fmt.Printf("    Resolution: %s\n", f.Resolution)
	}
	if f.HelpURL != "" {
		fmt.Printf("    Docs: %s\n", f.HelpURL)
	}
}
