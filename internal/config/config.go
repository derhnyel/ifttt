package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/scan"
)

type Config struct {
	Parallelism string   `yaml:"parallelism"`
	Verbose     bool     `yaml:"verbose"`
	Ignores     []string `yaml:"ignores"`
	SkipDirs    []string `yaml:"skip_directories"`
	Remotes     []Remote `yaml:"remotes"`
	Directives  struct {
		Prefix string `yaml:"prefix"`
	} `yaml:"directives"`
	Languages struct {
		PythonDocstrings *bool `yaml:"python_docstrings"`
	} `yaml:"languages"`
	Rules struct {
		UnknownDirective string `yaml:"unknown_directive"`
		CombinedDiff     string `yaml:"combined_diff"`
		CodeOnly         bool   `yaml:"code_only"`
	} `yaml:"rules"`
	Output struct {
		Format string `yaml:"format"`
		Path   string `yaml:"path"`
	} `yaml:"output"`
	BaseDir                 string `yaml:"-"`
	verboseSet, codeOnlySet bool
}

// Preserve public bool fields while distinguishing an omitted YAML setting from
// an explicit false value when cascading configuration files.
func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	type plain Config
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	var presence struct {
		Verbose *bool `yaml:"verbose"`
		Rules   struct {
			CodeOnly *bool `yaml:"code_only"`
		} `yaml:"rules"`
	}
	if err := node.Decode(&presence); err != nil {
		return err
	}
	*c = Config(decoded)
	c.verboseSet = presence.Verbose != nil
	c.codeOnlySet = presence.Rules.CodeOnly != nil
	return nil
}

type Remote struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	Repo       string `yaml:"repo"`
	DefaultRef string `yaml:"default_ref"`
	TokenEnv   string `yaml:"token_env"`
	BaseURL    string `yaml:"base_url"`
}

// Decode validates one repository-root configuration without filesystem access.
// A missing committed configuration uses the same defaults as a local run.
func Decode(data []byte, repositoryRoot ...string) (Config, error) {
	var cfg Config
	if len(data) > 1<<20 {
		return cfg, errors.New("configuration exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return cfg, err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return cfg, err
		}
		return cfg, errors.New("configuration must contain one YAML document")
	}
	root := "."
	if len(repositoryRoot) > 0 {
		root = repositoryRoot[0]
	}
	cfg = normalizeLayer(cfg, root, root)
	cfg.BaseDir = root
	if err := Validate(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func Load(repoRoot string) (Config, error) {
	abs, err := filepath.Abs(repoRoot)
	if err != nil {
		return Config{}, err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}

	type layer struct {
		cfg Config
		dir string
	}
	var layers []layer
	dir := abs
	for {
		p := filepath.Join(dir, ".ifttt-lint.yaml")
		data, err := os.ReadFile(p)
		if err == nil {
			var cfg Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("%s: %w", p, err)
			}
			layers = append(layers, layer{cfg: cfg, dir: dir})
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("read %s: %w", p, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if len(layers) == 0 {
		var cfg Config
		cfg.BaseDir = abs
		if err := Validate(&cfg); err != nil {
			return cfg, err
		}
		return cfg, fs.ErrNotExist
	}

	rootDir := layers[len(layers)-1].dir
	merged := normalizeLayer(layers[len(layers)-1].cfg, layers[len(layers)-1].dir, rootDir)
	for i := len(layers) - 2; i >= 0; i-- {
		high := normalizeLayer(layers[i].cfg, layers[i].dir, rootDir)
		merged = Merge(high, merged)
	}
	merged.BaseDir = rootDir
	if err := Validate(&merged); err != nil {
		return Config{}, err
	}
	return merged, nil
}

func Merge(high, low Config) Config { // high precedence overrides low
	out := low
	if high.Parallelism != "" {
		out.Parallelism = high.Parallelism
	}
	if high.verboseSet || high.Verbose {
		out.Verbose = high.Verbose
		out.verboseSet = true
	}
	out.Ignores = appendUnique(out.Ignores, high.Ignores)
	out.SkipDirs = appendUnique(out.SkipDirs, high.SkipDirs)
	if high.Directives.Prefix != "" {
		out.Directives.Prefix = high.Directives.Prefix
	}
	if high.Languages.PythonDocstrings != nil {
		out.Languages.PythonDocstrings = high.Languages.PythonDocstrings
	}
	if high.Rules.UnknownDirective != "" {
		out.Rules.UnknownDirective = high.Rules.UnknownDirective
	}
	if high.Rules.CombinedDiff != "" {
		out.Rules.CombinedDiff = high.Rules.CombinedDiff
	}
	if high.codeOnlySet || high.Rules.CodeOnly {
		out.Rules.CodeOnly = high.Rules.CodeOnly
		out.codeOnlySet = true
	}
	if high.Output.Format != "" {
		out.Output.Format = high.Output.Format
	}
	if high.Output.Path != "" {
		out.Output.Path = high.Output.Path
	}
	if out.Directives.Prefix == "" {
		out.Directives.Prefix = low.Directives.Prefix
	}
	if len(high.Remotes) > 0 {
		out.Remotes = append(append([]Remote{}, low.Remotes...), high.Remotes...)
	}
	return out
}

func Validate(c *Config) error {
	if c.Parallelism != "" && !strings.EqualFold(c.Parallelism, "auto") {
		n, err := strconv.Atoi(c.Parallelism)
		if err != nil || n < 1 {
			return errors.New("parallelism must be auto or a positive integer")
		}
	}

	if c.Output.Format == "" {
		c.Output.Format = "text"
	}
	if !inSet(c.Output.Format, "text", "json", "sarif", "diagnostic-ls", "diagnostic", "dls") {
		return errors.New("output.format must be text|json|sarif|diagnostic-ls")
	}
	if c.Rules.UnknownDirective != "" && !inSet(c.Rules.UnknownDirective, "warn", "error", "ignore") {
		return errors.New("rules.unknown_directive invalid")
	}
	if strings.TrimSpace(c.Rules.CombinedDiff) == "" {
		c.Rules.CombinedDiff = "skip_with_note"
	}
	if c.Rules.CombinedDiff != "" && !inSet(c.Rules.CombinedDiff, "skip_with_note", "error", "ignore", "parent") {
		return errors.New("rules.combined_diff invalid")
	}
	if strings.TrimSpace(c.Directives.Prefix) == "" {
		c.Directives.Prefix = core.DefaultDirectivePrefix
	}
	if len(c.SkipDirs) == 0 {
		c.SkipDirs = append([]string{}, scan.DefaultSkippedDirs...)
	}
	for _, remote := range c.Remotes {
		if err := validateRemote(remote); err != nil {
			return err
		}
	}
	return nil
}

func inSet(v string, s ...string) bool {
	for _, x := range s {
		if strings.EqualFold(v, x) {
			return true
		}
	}
	return false
}

func normalizeLayer(cfg Config, cfgDir, rootDir string) Config {
	cfg.SkipDirs = normalizePaths(cfg.SkipDirs, cfgDir, rootDir)
	cfg.Ignores = normalizeIgnores(cfg.Ignores, cfgDir, rootDir)
	return cfg
}

func validateRemote(remote Remote) error {
	typeName := strings.ToLower(strings.TrimSpace(remote.Type))
	if typeName == "" {
		typeName = "github"
	}
	switch typeName {
	case "github":
		if strings.TrimSpace(remote.Repo) == "" {
			return errors.New("remotes.repo is required for github remote")
		}
		if !strings.Contains(remote.Repo, "/") {
			return fmt.Errorf("remotes.repo must be owner/repo format: %s", remote.Repo)
		}
	default:
		return fmt.Errorf("unsupported remote type %q", remote.Type)
	}
	return nil
}

func normalizePaths(entries []string, cfgDir, rootDir string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		rel := relativize(raw, cfgDir, rootDir)
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	return out
}

func normalizeIgnores(entries []string, cfgDir, rootDir string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		pathPart := raw
		label := ""
		if idx := strings.Index(raw, "#"); idx >= 0 {
			pathPart = raw[:idx]
			label = raw[idx:]
		}
		rel := relativize(pathPart, cfgDir, rootDir)
		entry := rel + label
		if _, dup := seen[entry]; dup {
			continue
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	return out
}

func relativize(value, cfgDir, rootDir string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") {
		return value
	}
	var abs string
	if filepath.IsAbs(value) {
		abs = value
	} else {
		abs = filepath.Join(cfgDir, value)
	}
	if rootDir == "" {
		return filepath.Clean(value)
	}
	rel, err := filepath.Rel(rootDir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(filepath.Clean(value))
	}
	return filepath.ToSlash(filepath.Clean(rel))
}

func appendUnique(base, extra []string) []string {
	if len(extra) == 0 {
		return append([]string{}, base...)
	}
	set := map[string]struct{}{}
	var out []string
	for _, v := range base {
		if _, ok := set[v]; ok {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range extra {
		if _, ok := set[v]; ok {
			continue
		}
		set[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// PythonDocstringsEnabled defaults to true and preserves explicit false in layers.
func (c Config) PythonDocstringsEnabled() bool {
	return c.Languages.PythonDocstrings == nil || *c.Languages.PythonDocstrings
}
