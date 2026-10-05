package engine

import (
	"fmt"
	"testing"
)

// LINT.IfChange(conditional_target_structure)
func TestConditionalTargetStructureWithoutCoChanges(t *testing.T) {
	for _, rule := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
		for _, selector := range []string{"target.go#API", "missing.go", "valid.go#API"} {
			for _, mode := range []string{"scan", "suppressed", "unselected"} {
				t.Run(rule+"/"+selector+"/"+mode, func(t *testing.T) {
					argument := fmt.Sprintf("[%q]", selector)
					if rule == "ForbidChange" {
						argument = fmt.Sprintf("%q", selector)
					}
					directive := "// SENTRY." + rule + "(" + argument + ")"
					files := map[string]string{
						"source.go": directive + "\nbody\n",
						"target.go": "body\n",
						"valid.go":  "// SENTRY.Label(\"API\")\nbody\n// SENTRY.EndLabel\n",
					}
					var diff []string
					opts := Options{StructuralFiles: []string{"source.go"}}
					if mode != "scan" {
						diff = []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,2 +1,2 @@", " " + directive, "-old", "+body"}
						if mode == "suppressed" {
							opts.SuppressCoChanges = true
						} else {
							opts.SourceFiles = []string{"target.go"}
							opts.ReverseCandidates = []string{"source.go"}
							opts.DependencyConfigChanged = func(string) bool { return true }
						}
					}
					result, code, _ := runLintWithSetup(t, files, diff, opts)
					if selector == "valid.go#API" {
						if code != 0 || len(result.Findings) != 0 {
							t.Fatalf("inactive rule required a body edit: %+v", result.Findings)
						}
						return
					}
					want := "label_missing"
					if selector == "missing.go" {
						want = "error"
					}
					if code != 1 || len(result.Findings) != 1 || result.Findings[0].RuleID != want || result.Findings[0].File != "source.go" {
						t.Fatalf("inactive edit checks lost structural validation: code=%d findings=%+v", code, result.Findings)
					}
				})
			}
		}
	}
}

func TestConditionalTargetsInSkippedDirectories(t *testing.T) {
	for _, rule := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
		for _, directory := range []string{"build", "excluded"} {
			for _, labelled := range []bool{false, true} {
				for _, exists := range []bool{false, true} {
					for _, active := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/labelled=%v/exists=%v/active=%v", rule, directory, labelled, exists, active), func(t *testing.T) {
							path := directory + "/target.go"
							selector := path
							if labelled {
								selector += "#API"
							}
							argument := fmt.Sprintf("[%q]", selector)
							if rule == "ForbidChange" {
								argument = fmt.Sprintf("%q", selector)
							}
							directive := "// SENTRY." + rule + "(" + argument + ")"
							files := map[string]string{"source.go": directive + "\nbody\n"}
							if exists {
								files[path] = "// SENTRY.Label(\"API\")\nbody\n// SENTRY.EndLabel\n"
							}
							opts := Options{StructuralFiles: []string{"source.go"}}
							if directory == "excluded" {
								opts.SkipDirs = []string{directory}
							}
							var diff []string
							if active {
								diff = []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,2 +1,2 @@", " " + directive, "-old", "+body"}
							}
							result, code, _ := runLintWithSetup(t, files, diff, opts)
							if code != 0 || len(result.Findings) != 0 {
								t.Fatalf("skipped target was evaluated: code=%d findings=%+v", code, result.Findings)
							}
						})
					}
				}
			}
		}
	}
}

// LINT.ThenChange(//internal/engine/rules.go:conditional_target_structure, //internal/engine/rules.go:conditional_source_trigger, //internal/engine/engine.go:conditional_target_structure, //internal/engine/engine.go:target_exclusions)

func TestConditionalForbidDeletionStillUsesChangeEvidence(t *testing.T) {
	for _, selector := range []string{"target.go", "target.go#API"} {
		t.Run(selector, func(t *testing.T) {
			directive := fmt.Sprintf("// SENTRY.ForbidChange(%q)", selector)
			files := map[string]string{"source.go": directive + "\nbody\n"}
			diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,2 +1,2 @@", " " + directive, "-old", "+body", "diff --git a/target.go b/target.go", "deleted file mode 100644", "--- a/target.go", "+++ /dev/null", "@@ -1 +0,0 @@", "-old"}
			result, code, _ := runLintWithSetup(t, files, diff, Options{})
			if code != 1 || !hasRule(result, "source.go", "forbid_change") {
				t.Fatalf("structural validation hid forbidden deletion: %+v", result.Findings)
			}
		})
	}
}
