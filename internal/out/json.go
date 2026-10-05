package out

import (
	"encoding/json"
	"os"
	"path/filepath"

	core "github.com/derhnyel/ifttt/internal"
)

type JSON struct {
	Path                 string
	IncludeWorkspaceRoot bool
}

type jsonFinding struct {
	Repository   string `json:"repository,omitempty"`
	BaseRevision string `json:"baseRevision,omitempty"`
	HeadRevision string `json:"headRevision,omitempty"`
	TargetPath   string `json:"targetPath,omitempty"`
	TargetLabel  string `json:"targetLabel,omitempty"`
	RuleID       string `json:"ruleId"`
	Severity     string `json:"severity"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Message      string `json:"message"`
	HelpURL      string `json:"helpUrl,omitempty"`
	Suppressed   bool   `json:"suppressed,omitempty"`
	Summary      string `json:"summary,omitempty"`
	Resolution   string `json:"resolution,omitempty"`
}

func (w JSON) Write(fs []core.Finding, suppressed []core.Finding) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	payload := map[string]any{"errors": toJSONFindings(fs)}
	if w.IncludeWorkspaceRoot {
		root, err := os.Getwd()
		if err != nil {
			return err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		payload["workspaceRoot"] = root
	}
	if len(suppressed) > 0 {
		payload["suppressed"] = toJSONFindings(suppressed)
	}
	return enc.Encode(payload)
}

func toJSONFindings(fs []core.Finding) []jsonFinding {
	out := make([]jsonFinding, len(fs))
	for i, f := range fs {
		out[i] = jsonFinding{
			Repository:   f.Repository,
			BaseRevision: f.BaseRevision,
			HeadRevision: f.HeadRevision,
			RuleID:       f.RuleID,
			TargetPath:   f.TargetPath,
			TargetLabel:  f.TargetLabel,
			Severity:     f.Severity,
			File:         f.File,
			Line:         f.Line,
			Message:      f.Message,
			HelpURL:      f.HelpURL,
			Suppressed:   f.Suppressed,
			Summary:      f.Summary,
			Resolution:   f.Resolution,
		}
	}
	return out
}
