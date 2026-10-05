package ifttt

type FileChanges struct {
	// DirectivePrefix scopes removed metadata to its owning committed repository.
	DirectivePrefix string
	// ExistingGuards classifies added opening directives when original source
	// can be reconstructed. Nil means correspondence was not computed.
	ExistingGuards map[int]bool
	// ContentChanged preserves authoritative snapshot changes without text hunks.
	ContentChanged bool
	TypeChanged    bool
	// Opaque means content changed but native line evidence is unavailable.
	Opaque       bool
	File         string
	OldFile      string
	Deleted      bool
	Renamed      bool
	AddedLines   map[int]bool
	RemovedLines map[int]bool
	// RemovedInNew records deletion anchors in current-file coordinates.
	RemovedInNew map[int]string
	// For code-only mode we keep the actual hunk texts by line number
	AddedByLine      map[int]string
	RemovedByLine    map[int]string
	HasDirectiveHint bool
}

type LineRange struct {
	StartLine int
	EndLine   int
}

type HunkLine struct {
	Kind byte   // '+', '-', ' '
	Text string // content without prefix
}

type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []HunkLine
}

type FilePatch struct {
	OldPath string
	NewPath string
	Chunks  []Hunk
}

// Directive kinds
const (
	Unknown    = "Unknown"
	IfChange   = "IfChange"
	ThenChange = "ThenChange"
	Label      = "Label"
	EndLabel   = "EndLabel"
	RequireAny = "RequireAny"
	RequireAll = "RequireAll"
	Forbid     = "Forbid"
	Disable    = "Disable"
	Enable     = "Enable"
	Ignore     = "Ignore"
	// LINT.IfChange(match_kind)
	Match = "Match"
	// LINT.ThenChange(//internal/parse/match.go:match_contract, //test/integration/match_test.go:match_contract)
)

type LintDirective struct {
	Kind string `json:"kind"`
	Line int    `json:"line"`
	// Optional fields depend on the directive kind.
	Label  string   `json:"label,omitempty"`
	Name   string   `json:"name,omitempty"`
	Target string   `json:"target,omitempty"`
	List   []string `json:"list,omitempty"`
	// Match uses two labelled references and an optional extraction pattern.
	Pattern string `json:"pattern,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Finding struct {
	Repository   string
	BaseRevision string
	HeadRevision string
	TargetPath   string
	TargetLabel  string
	RuleID       string
	Severity     string // error|warning|info
	File         string
	Line         int
	Message      string
	HelpURL      string
	Suppressed   bool
	Summary      string
	Resolution   string
}

type TargetRef struct{ Raw, Path, Label string }

type ResultWriter interface {
	Write([]Finding, []Finding) error
}
