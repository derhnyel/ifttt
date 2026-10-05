package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Exercise complete dependency chains, including a mixture of native backends.
func TestChangeSetThreeRepositoryChain(t *testing.T) {
	for _, middleBackend := range []string{"git", "jj"} {
		t.Run(middleBackend, func(t *testing.T) {
			a, b, c := newExampleRepo(t, "git"), newExampleRepo(t, middleBackend), newExampleRepo(t, "git")
			body := func(target string) string {
				return "// LINT.IfChange(API)\nconst version = 1\n// LINT.ThenChange(" + target + ")\n"
			}
			aBody, bBody, cBody := body("github://acme/middle/api.go#API"), body("github://acme/end/api.go#API"), body("")
			a.write(t, "api.go", aBody)
			b.write(t, "api.go", bBody)
			c.write(t, "api.go", cBody)
			aBase, bBase, cBase := commitExample(t, a, "git"), commitExample(t, b, middleBackend), commitExample(t, c, "git")
			run := func(aHead, bHead, cHead string, code int) string {
				t.Helper()
				manifest := combinationManifest(t, []map[string]string{
					{"repo": "acme/start", "path": a.dir, "vcs": "git", "base": aBase, "head": aHead},
					{"repo": "acme/middle", "path": b.dir, "vcs": middleBackend, "base": bBase, "head": bHead},
					{"repo": "acme/end", "path": c.dir, "vcs": "git", "base": cBase, "head": cHead},
				})
				return requireCode(t, a, "", code, "--change-set", manifest, "--format=json")
			}
			run(aBase, bBase, cBase, 0)
			a.write(t, "api.go", strings.Replace(aBody, "= 1", "= 2", 1))
			aHead := commitExample(t, a, "git")
			out := run(aHead, bBase, cBase, 1)
			assertRepositoryRule(t, out, "acme/start", "then_label_missing")
			b.write(t, "api.go", strings.Replace(bBody, "= 1", "= 2", 1))
			bHead := commitExample(t, b, middleBackend)
			assertRepositoryRule(t, run(aHead, bHead, cBase, 1), "acme/middle", "then_label_missing")
			c.write(t, "api.go", strings.Replace(cBody, "= 1", "= 2", 1))
			run(aHead, bHead, cBase, 1) // Dirty contents do not complete the chain.
			cHead := commitExample(t, c, "git")
			run(aHead, bHead, cHead, 0)
			if err := os.Remove(filepath.Join(c.dir, "api.go")); err != nil {
				t.Fatal(err)
			}
			deletedHead := commitExample(t, c, "git")
			// Unchanged incoming sources must still report a deleted final target.
			manifest := combinationManifest(t, []map[string]string{
				{"repo": "acme/start", "path": a.dir, "vcs": "git", "base": aHead, "head": aHead},
				{"repo": "acme/middle", "path": b.dir, "vcs": middleBackend, "base": bHead, "head": bHead},
				{"repo": "acme/end", "path": c.dir, "vcs": "git", "base": cHead, "head": deletedHead},
			})
			assertRepositoryRule(t, requireCode(t, a, "", 1, "--change-set", manifest, "--format=json"), "acme/middle", "then_missing")
		})
	}
}

func combinationManifest(t *testing.T, repositories []map[string]string) string {
	t.Helper()
	data, err := yaml.Marshal(map[string]any{"version": 1, "repositories": repositories})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "changes.yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRepositoryRule(t *testing.T, output, repository, rule string) {
	t.Helper()
	var report struct {
		Errors []struct{ Repository, RuleID string }
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	for _, finding := range report.Errors {
		if finding.Repository == repository && finding.RuleID == rule {
			return
		}
	}
	t.Fatalf("missing %s in repository %s: %s", rule, repository, output)
}

func TestChangeSetMixedLocalRemoteConditionsAndSuppression(t *testing.T) {
	for _, targetBackend := range []string{"git", "jj"} {
		t.Run(targetBackend, func(t *testing.T) {
			for _, directive := range []string{"RequireAny", "RequireAll", "ForbidChange"} {
				t.Run(directive, func(t *testing.T) {
					a, b := newExampleRepo(t, "git"), newExampleRepo(t, targetBackend)
					src := "// LINT.IfChange(API)\nconst version = 1\n// LINT." + directive + "([\"//local.go:API\", \"github://acme/target/target.go#API\"])\n// LINT.ThenChange()\n"
					if directive == "ForbidChange" {
						src = strings.Replace(src, `LINT.ForbidChange(["//local.go:API", "github://acme/target/target.go#API"])`, "LINT.ForbidChange(\"//local.go:API\")\n// LINT.ForbidChange(\"github://acme/target/target.go#API\")", 1)
					}
					targetBody := "// LINT.IfChange(API)\nconst version = 1\n// LINT.ThenChange()\n"
					a.write(t, "source.go", src)
					a.write(t, "local.go", targetBody)
					b.write(t, "target.go", targetBody)
					aBase, bBase := commitExample(t, a, "git"), commitExample(t, b, targetBackend)
					a.write(t, "source.go", strings.Replace(src, "= 1", "= 2", 1))
					aHead := commitExample(t, a, "git")
					run := func(aHead, bHead string, code int, args ...string) string {
						t.Helper()
						manifest := combinationManifest(t, []map[string]string{
							{"repo": "acme/source", "path": a.dir, "vcs": "git", "base": aBase, "head": aHead},
							{"repo": "acme/target", "path": b.dir, "vcs": targetBackend, "base": bBase, "head": bHead},
						})
						return requireCode(t, a, "", code, append([]string{"--change-set", manifest, "--format=json"}, args...)...)
					}
					rule := map[string]string{"RequireAny": "require_any_missing", "RequireAll": "require_all_missing", "ForbidChange": "forbid_change"}[directive]
					if directive == "ForbidChange" {
						run(aHead, bBase, 0)
					} else {
						assertRepositoryRule(t, run(aHead, bBase, 1), "acme/source", rule)
					}
					// A remote edit satisfies Any, leaves All unsatisfied and violates Forbid.
					b.write(t, "target.go", strings.Replace(targetBody, "= 1", "= 2", 1))
					bHead := commitExample(t, b, targetBackend)
					if directive == "RequireAny" {
						run(aHead, bHead, 0)
					} else {
						assertRepositoryRule(t, run(aHead, bHead, 1), "acme/source", rule)
					}
					if directive == "RequireAll" {
						a.write(t, "local.go", strings.Replace(targetBody, "= 1", "= 2", 1))
						run(commitExample(t, a, "git"), bHead, 0)
						a.write(t, "local.go", targetBody)
					}
					// Suppression is read from the committed source, never its dirty copy.
					a.write(t, "source.go", "// LINT.Disable(\""+rule+"\")\n"+strings.Replace(src, "= 1", "= 2", 1))
					suppressedHead := commitExample(t, a, "git")
					suppressedTargetHead := bHead
					if directive == "RequireAny" {
						suppressedTargetHead = bBase
					}
					output := run(suppressedHead, suppressedTargetHead, 0, "--list-suppressed")
					var report struct{ Errors, Suppressed []struct{ RuleID string } }
					if err := json.Unmarshal([]byte(output), &report); err != nil {
						t.Fatal(err)
					}
					if len(report.Errors) != 0 || len(report.Suppressed) != 1 || report.Suppressed[0].RuleID != rule {
						t.Fatalf("missing committed suppression: %s", output)
					}
					a.write(t, "source.go", strings.Replace(src, "= 1", "= 2", 1))
					run(suppressedHead, suppressedTargetHead, 0)
					// Suppressing the edit rule cannot hide a missing remote label.
					b.write(t, "target.go", strings.Replace(targetBody, "IfChange(API)", "IfChange(RENAMED)", 1))
					assertRepositoryRule(t, run(suppressedHead, commitExample(t, b, targetBackend), 1), "acme/source", "label_missing")
				})
			}
		})
	}
}
