package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/parse"
)

// LINT.IfChange(directive_inspection)
// Inspection uses the same comment parser as lint, including editor contents
// that are not saved yet. It does not evaluate dependencies or modify files.
func runInspect(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	stdin := flags.Bool("stdin", false, "inspect editor contents from stdin instead of reading the file")
	var styles multiFlag
	flags.Var(&styles, "comment-style", "override line comments using .ext=prefix (repeatable)")
	if err := flags.Parse(reorderFlagArguments(flags, args)); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("inspect requires one source file path")
	}
	if _, err := loadDirectiveConfiguration(); err != nil {
		return err
	}
	if err := applyCommentStyleOverrides(styles); err != nil {
		return err
	}
	provider := parse.Provider{}
	if *stdin {
		const limit = 16 << 20
		content, err := io.ReadAll(io.LimitReader(os.Stdin, limit+1))
		if err != nil {
			return err
		}
		if len(content) > limit {
			return fmt.Errorf("inspect input exceeds 16 MiB")
		}
		provider.ReadFile = func(string) ([]byte, error) { return content, nil }
	}
	dirs, err := provider.Parse(flags.Arg(0))
	// Return partial results for malformed structure so hover can explain it.
	// File/provider failures have no parsed directives and remain operational errors.
	if err != nil && dirs == nil {
		return err
	}
	if dirs == nil {
		dirs = []core.LintDirective{}
	}
	result := struct {
		Prefix     string               `json:"prefix"`
		Directives []core.LintDirective `json:"directives"`
		ParseError string               `json:"parseError,omitempty"`
	}{Prefix: core.CurrentDirectiveSyntax().Prefix, Directives: dirs}
	if err != nil {
		result.ParseError = err.Error()
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

// LINT.ThenChange(//test/integration/inspect_test.go:directive_inspection, //README.md:inspect_command)
