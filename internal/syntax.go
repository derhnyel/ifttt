package ifttt

import (
	"strings"
	"sync"
)

// DefaultDirectivePrefix selects the standard Google-style directive grammar.
const DefaultDirectivePrefix = "LINT"

type DirectiveSyntax struct {
	Prefix          string
	PrefixDot       string
	TokenIfChange   string
	TokenThenChange string
	TokenLabel      string
}

var (
	syntaxMu      sync.RWMutex
	currentSyntax = makeSyntax(DefaultDirectivePrefix)
)

func CurrentDirectiveSyntax() DirectiveSyntax {
	syntaxMu.RLock()
	defer syntaxMu.RUnlock()
	return currentSyntax
}

func SetDirectivePrefix(prefix string) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = DefaultDirectivePrefix
	}
	syntaxMu.Lock()
	currentSyntax = makeSyntax(prefix)
	syntaxMu.Unlock()
}

func makeSyntax(prefix string) DirectiveSyntax {
	prefixDot := prefix + "."
	token := func(name string) string { return prefix + "." + name }

	return DirectiveSyntax{
		Prefix:          prefix,
		PrefixDot:       prefixDot,
		TokenIfChange:   token("IfChange"),
		TokenThenChange: token("ThenChange"),
		TokenLabel:      token("Label"),
	}
}
