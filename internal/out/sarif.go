package out

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	core "github.com/derhnyel/ifttt/internal"
)

type SARIF struct{}

func (SARIF) Write(active []core.Finding, suppressed []core.Finding) error {
	// minimal SARIF v2.1.0 structure
	results := toResults(active, false)
	if len(suppressed) > 0 {
		results = append(results, toResults(suppressed, true)...)
	}
	t := map[string]any{
		"$schema": "https://schemastore.azurewebsites.net/schemas/json/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []any{map[string]any{
			"tool":    map[string]any{"driver": map[string]any{"name": "ifttt"}},
			"results": results,
		}},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(t)
}

func toResults(fs []core.Finding, suppressed bool) []any {
	var out []any
	for _, f := range fs {
		props := map[string]any{}
		if f.Repository != "" {
			props["repository"] = f.Repository
			props["baseRevision"] = f.BaseRevision
			props["headRevision"] = f.HeadRevision
		}
		message := map[string]any{"text": f.Message}
		result := map[string]any{
			"ruleId":  f.RuleID,
			"level":   f.Severity,
			"message": message,
			"locations": []any{map[string]any{"physicalLocation": map[string]any{
				"artifactLocation": map[string]any{"uri": f.File},
				"region":           map[string]any{"startLine": f.Line},
			}}},
		}
		if uri := helpURIForFinding(f); uri != "" {
			result["helpUri"] = uri
		}
		if f.Summary != "" {
			result["shortDescription"] = map[string]any{"text": f.Summary}
			props["summary"] = f.Summary
		}
		if f.Resolution != "" {
			props["resolution"] = f.Resolution
			message["markdown"] = fmt.Sprintf("%s\n\n**Resolution:** %s", f.Message, f.Resolution)
		}
		if suppressed {
			result["level"] = "none"
			result["suppressions"] = []any{
				map[string]any{
					"kind":          "inSource",
					"justification": "Suppressed via directive",
				},
			}
			props["suppressed"] = true
		}
		if len(props) > 0 {
			result["properties"] = props
		}
		out = append(out, result)
	}
	return out
}

var (
	reThenTarget    = regexp.MustCompile(`'([^'#]+)(?:#([^']+))?'`)
	reLabelNotFound = regexp.MustCompile(`label '([^']+)' not found in '([^']+)'`)
)

func helpURIForFinding(f core.Finding) string {
	if f.HelpURL != "" {
		return f.HelpURL
	}
	switch f.RuleID {
	case "then_missing", "then_label_missing":
		if m := reThenTarget.FindStringSubmatch(f.Message); len(m) >= 2 {
			path := m[1]
			if len(m) >= 3 && m[2] != "" {
				return "ifttt://" + path + "#" + m[2]
			}
			return "ifttt://" + path
		}
	case "label_missing":
		if m := reLabelNotFound.FindStringSubmatch(f.Message); len(m) >= 3 {
			return "ifttt://" + m[2] + "#" + m[1]
		}
	}
	return ""
}
