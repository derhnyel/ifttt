package out

import (
	"encoding/json"
	"os"
	"sort"
	"strings"

	core "github.com/derhnyel/ifttt/internal"
)

// DiagnosticLS emits diagnostics compatible with diagnostic-languageserver.
type DiagnosticLS struct{}

type dlsRange struct {
	Start dlsPosition `json:"start"`
	End   dlsPosition `json:"end"`
}

type dlsPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type dlsDiagnostic struct {
	Repository   string   `json:"repository,omitempty"`
	BaseRevision string   `json:"baseRevision,omitempty"`
	HeadRevision string   `json:"headRevision,omitempty"`
	Range        dlsRange `json:"range"`
	Message      string   `json:"message"`
	Severity     int      `json:"severity"`
	Code         string   `json:"code,omitempty"`
	Source       string   `json:"source,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Resolution   string   `json:"resolution,omitempty"`
	HelpURL      string   `json:"helpUrl,omitempty"`
	Suppressed   bool     `json:"suppressed,omitempty"`
}

type dlsItem struct {
	File        string          `json:"file"`
	Diagnostics []dlsDiagnostic `json:"diagnostics"`
}

func (DiagnosticLS) Write(active []core.Finding, suppressed []core.Finding) error {
	payload := map[string]any{
		"items": groupDiagnostics(active, false),
	}
	if len(suppressed) > 0 {
		payload["suppressed"] = groupDiagnostics(suppressed, true)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func groupDiagnostics(fs []core.Finding, suppress bool) []dlsItem {
	if len(fs) == 0 {
		return nil
	}
	m := map[string][]dlsDiagnostic{}
	for _, f := range fs {
		diag := dlsDiagnostic{
			Repository:   f.Repository,
			BaseRevision: f.BaseRevision,
			HeadRevision: f.HeadRevision,
			Range: dlsRange{
				Start: dlsPosition{Line: max(f.Line-1, 0)},
				End:   dlsPosition{Line: max(f.Line-1, 0)},
			},
			Message:    f.Message,
			Severity:   severityToDLS(f.Severity),
			Code:       f.RuleID,
			Source:     "ifttt",
			Summary:    f.Summary,
			Resolution: f.Resolution,
			HelpURL:    f.HelpURL,
			Suppressed: suppress,
		}
		m[f.File] = append(m[f.File], diag)
	}
	files := make([]string, 0, len(m))
	for k := range m {
		files = append(files, k)
	}
	sort.Strings(files)
	items := make([]dlsItem, 0, len(files))
	for _, path := range files {
		items = append(items, dlsItem{
			File:        path,
			Diagnostics: m[path],
		})
	}
	return items
}

func severityToDLS(level string) int {
	switch strings.ToLower(level) {
	case "error":
		return 1
	case "warning":
		return 2
	case "info":
		return 3
	default:
		return 4
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
