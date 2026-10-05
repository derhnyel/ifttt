package engine

import (
	"strings"
	"testing"
)

func basicDiff(path, oldBody, newBody string) []string {
	return []string{"diff --git a/" + path + " b/" + path, "--- a/" + path, "+++ b/" + path, "@@ -1,3 +1,3 @@", " // SENTRY.IfChange(\"X\")", "-" + oldBody, "+" + newBody, " // SENTRY.ThenChange(\"target.go\")"}
}

func TestNewPairDoesNotRequireCochange(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"X\")\nnew body\n// SENTRY.ThenChange(\"target.go\")\n", "target.go": "stable\n"}
	diff := []string{"diff --git a/source.go b/source.go", "--- /dev/null", "+++ b/source.go", "@@ -0,0 +1,3 @@", "+// SENTRY.IfChange(\"X\")", "+new body", "+// SENTRY.ThenChange(\"target.go\")"}
	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code != 0 {
		t.Fatalf("new contract triggered: %+v", res.Findings)
	}
}

func TestNestedPairsEnforceOuterAndInner(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"OUTER\")\n// SENTRY.IfChange(\"INNER\")\nchanged\n// SENTRY.ThenChange(\"inner.go\")\n// SENTRY.ThenChange(\"outer.go\")\n", "inner.go": "stable\n", "outer.go": "stable\n"}
	diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,5 +1,5 @@", " // SENTRY.IfChange(\"OUTER\")", " // SENTRY.IfChange(\"INNER\")", "-old", "+changed", " // SENTRY.ThenChange(\"inner.go\")", " // SENTRY.ThenChange(\"outer.go\")"}
	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code != 1 || len(res.Findings) != 2 {
		t.Fatalf("both nested targets must be enforced: %+v", res.Findings)
	}
	for _, f := range res.Findings {
		if f.RuleID != "then_missing" {
			t.Fatalf("wrong nested result: %+v", f)
		}
	}
}

func TestExistingMissingTargetCheckedOnMetadataEdit(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"X\")\nbody\n// SENTRY.ThenChange(\"missing.go\")\n"}
	diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,3 +1,3 @@", " // SENTRY.IfChange(\"X\")", " body", "-// SENTRY.ThenChange(\"old.go\")", "+// SENTRY.ThenChange(\"missing.go\")"}
	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code != 1 || len(res.Findings) == 0 {
		t.Fatalf("invalid contract passed: %+v", res.Findings)
	}
}

func TestExtendedRulesEnforced(t *testing.T) {
	for _, rule := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
		t.Run(rule, func(t *testing.T) {
			arg := "[\"target.go\",\"other.go\"]"
			if rule == "ForbidChange" {
				arg = "\"target.go\""
			}
			directive := "// SENTRY." + rule + "(" + arg + ")"
			files := map[string]string{"source.go": directive + "\nchanged\n", "target.go": "updated\n", "other.go": "stable\n"}
			diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,2 +1,2 @@", " " + directive, "-old", "+changed", "diff --git a/target.go b/target.go", "--- a/target.go", "+++ b/target.go", "@@ -1 +1 @@", "-stable", "+updated"}
			res, code, _ := runLintWithSetup(t, files, diff, Options{})
			if rule == "RequireAny" {
				if code != 0 {
					t.Fatalf("any changed target should satisfy: %+v", res.Findings)
				}
			} else {
				want := "require_all_missing"
				if rule == "ForbidChange" {
					want = "forbid_change"
				}
				if code != 1 || len(res.Findings) != 1 || res.Findings[0].RuleID != want {
					t.Fatalf("rule %s not enforced: %+v", rule, res.Findings)
				}
			}
		})
	}
}

func TestDiffPreservesDirectoryNamedA(t *testing.T) {
	files := map[string]string{"a/source.go": "// SENTRY.IfChange(\"X\")\nchanged\n// SENTRY.ThenChange(\"target.go\")\n", "a/target.go": "stable\n"}
	res, code, _ := runLintWithSetup(t, files, basicDiff("a/source.go", "old", "changed"), Options{})
	if code != 1 || len(res.Findings) != 1 || res.Findings[0].File != "a/source.go" {
		t.Fatalf("directory stripped: %+v", res.Findings)
	}
}

func TestUnknownDirectivePolicy(t *testing.T) {
	for _, policy := range []string{"error", "warn", "ignore"} {
		files := map[string]string{"source.go": "// SENTRY.Mispelled()\n"}
		diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1 +1 @@", "-// old", "+// SENTRY.Mispelled()"}
		res, code, _ := runLintWithSetup(t, files, diff, Options{UnknownPolicy: policy})
		if policy == "error" && (code != 1 || len(res.Findings) != 1) {
			t.Fatalf("unknown not rejected: %+v", res.Findings)
		}
		if policy == "warn" && (code != 0 || len(res.Findings) != 1 || res.Findings[0].Severity != "warning") {
			t.Fatalf("unknown warning lost: %+v", res.Findings)
		}
		if policy == "ignore" && (code != 0 || len(res.Findings) != 0) {
			t.Fatalf("unknown ignore failed: %+v", res.Findings)
		}
	}
}

func TestDisableEnableScopes(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.Disable(\"then_missing\")\n// SENTRY.IfChange(\"X\")\nchanged\n// SENTRY.ThenChange(\"target.go\")\n// SENTRY.Enable(\"then_missing\")\n", "target.go": "stable\n"}
	diff := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,5 +1,5 @@", " // SENTRY.Disable(\"then_missing\")", " // SENTRY.IfChange(\"X\")", "-old", "+changed", " // SENTRY.ThenChange(\"target.go\")", " // SENTRY.Enable(\"then_missing\")"}
	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code != 0 || len(res.Findings) != 0 || len(res.Suppressed) != 1 {
		t.Fatalf("disable ignored: %+v", res)
	}
}

func TestDeletedTargetFindsUnchangedReferences(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"X\")\nbody\n// SENTRY.ThenChange(\"target.go\")\n"}
	diff := []string{"diff --git a/target.go b/target.go", "deleted file mode 100644", "--- a/target.go", "+++ /dev/null", "@@ -1 +0,0 @@", "-old"}
	res, code, _ := runLintWithSetup(t, files, diff, Options{})
	if code != 1 || len(res.Findings) != 1 || res.Findings[0].File != "source.go" {
		t.Fatalf("stale reference passed: %+v", res.Findings)
	}
}

func TestStableFindingOrder(t *testing.T) {
	provider := &memoryFileProvider{files: map[string][]byte{"source.go": []byte("// SENTRY.IfChange(\"X\")\nchanged\n// SENTRY.ThenChange([\"b.go\",\"a.go\"])\n"), "a.go": []byte("x"), "b.go": []byte("x")}}
	diff := strings.Join([]string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,3 +1,3 @@", " // SENTRY.IfChange(\"X\")", "-old", "+changed", " // SENTRY.ThenChange([\"b.go\",\"a.go\"])"}, "\n") + "\n"
	var previous string
	for i := 0; i < 20; i++ {
		res, code := Lint(diff, Options{Files: provider})
		if code != 1 || len(res.Findings) != 2 {
			t.Fatalf("expected two dependency violations before comparing order: code=%d findings=%+v", code, res.Findings)
		}
		var joined string
		for _, f := range res.Findings {
			joined += f.Message
		}
		if i > 0 && joined != previous {
			t.Fatalf("nondeterministic findings: %s vs %s", joined, previous)
		}
		previous = joined
	}
}

func TestDirectiveParsingReadsCurrentContentWithUnchangedStat(t *testing.T) {
	p := &memoryFileProvider{files: map[string][]byte{"a.go": []byte("// SENTRY.Label(\"ONE\")\n// SENTRY.EndLabel\n")}}
	first, err := loadDirectives(p, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	p.files["a.go"] = []byte("// SENTRY.Label(\"TWO\")\n// SENTRY.EndLabel\n")
	second, err := loadDirectives(p, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Name == second[0].Name || second[0].Name != "TWO" {
		t.Fatalf("stale content: %+v", second)
	}
}

func TestRemovedCodeContainingDirectivePrefixTriggers(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"X\")\n// SENTRY.ThenChange(\"target.go\")\n", "target.go": "stable\n"}
	patch := []string{"diff --git a/source.go b/source.go", "--- a/source.go", "+++ b/source.go", "@@ -1,3 +1,2 @@", " // SENTRY.IfChange(\"X\")", "-println(\"SENTRY.example\")", " // SENTRY.ThenChange(\"target.go\")"}
	res, code, _ := runLintWithSetup(t, files, patch, Options{})
	if code != 1 || len(res.Findings) != 1 {
		t.Fatalf("deleted code ignored: %+v", res.Findings)
	}
}

func TestSuppressionKeepsStructuralAndReverseErrors(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"X\")\nnew\n// SENTRY.ThenChange(\"target.go\")\n", "target.go": "stable\n"}
	res, code, _ := runLintWithSetup(t, files, basicDiff("source.go", "old", "new"), Options{SuppressCoChanges: true})
	if code != 0 || len(res.Findings) != 0 {
		t.Fatalf("not suppressed: %+v", res.Findings)
	}
	delete(files, "target.go")
	res, code, _ = runLintWithSetup(t, files, basicDiff("source.go", "old", "new"), Options{SuppressCoChanges: true})
	if code != 1 || len(res.Findings) != 1 {
		t.Fatalf("structural error suppressed: %+v", res.Findings)
	}
}
func TestSourceSelectionScopesCoChange(t *testing.T) {
	files := map[string]string{"source.go": "// SENTRY.IfChange(\"X\")\nnew\n// SENTRY.ThenChange(\"target.go\")\n", "target.go": "stable\n"}
	res, code, _ := runLintWithSetup(t, files, basicDiff("source.go", "old", "new"), Options{SourceFiles: []string{"target.go"}})
	if code != 0 || len(res.Findings) != 0 {
		t.Fatalf("unselected source triggered: %+v", res.Findings)
	}
}
