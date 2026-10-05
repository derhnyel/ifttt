package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	core "github.com/derhnyel/ifttt/internal"
	"github.com/derhnyel/ifttt/internal/comments"
	"github.com/derhnyel/ifttt/internal/parse"
)

var (
	rePathOnly        = regexp.MustCompile(`'([^'#]+)'`)
	rePathWithLabel   = regexp.MustCompile(`'([^']+?)#([^']+)'`)
	reLabelNotFound   = regexp.MustCompile(`label '([^']+)' not found in '([^']+)'`)
	placeholderMarker = "// TODO(iflint): update per directive"
)

func applyFixes(findings []core.Finding) (actions []string, errs []string) {
	for _, f := range findings {
		switch f.RuleID {
		case "then_missing":
			if path, ok := fixPathOnly(f); ok {
				if err := validateFixPath(path); err != nil {
					errs = append(errs, err.Error())
					continue
				}
				if err := ensureTargetPlaceholder(path, f.TargetLabel); err != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", path, err))
				} else {
					actions = append(actions, fmt.Sprintf("%s: inserted placeholder", path))
				}
			}
		case "then_label_missing":
			if path, label, ok := fixPathAndLabel(f); ok {
				if err := validateFixPath(path); err != nil {
					errs = append(errs, err.Error())
					continue
				}
				if err := ensureLabelPlaceholder(path, label); err != nil {
					errs = append(errs, fmt.Sprintf("%s#%s: %v", path, label, err))
				} else {
					actions = append(actions, fmt.Sprintf("%s: created label %s", path, label))
				}
			}
		case "label_missing":
			if label, path, ok := fixLabelNotFound(f); ok {
				if err := validateFixPath(path); err != nil {
					errs = append(errs, err.Error())
					continue
				}
				if labelExists(path, label) {
					continue
				}
				if err := ensureLabelBlock(path, label); err != nil {
					errs = append(errs, fmt.Sprintf("%s#%s: %v", path, label, err))
				} else {
					actions = append(actions, fmt.Sprintf("%s: created label %s", path, label))
				}
			}
		}
	}
	return actions, errs
}

func extractPathOnly(msg string) (string, bool) {
	m := rePathOnly.FindStringSubmatch(msg)
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}

func extractPathAndLabel(msg string) (string, string, bool) {
	m := rePathWithLabel.FindStringSubmatch(msg)
	if len(m) < 3 {
		return "", "", false
	}
	return m[1], m[2], true
}

func extractLabelNotFound(msg string) (string, string, bool) {
	m := reLabelNotFound.FindStringSubmatch(msg)
	if len(m) < 3 {
		return "", "", false
	}
	return m[1], m[2], true
}

func ensureFilePlaceholder(path string) error {
	if path == "" {
		return nil
	}
	text, mode, err := readFile(path)
	if err != nil {
		return err
	}
	marker := fixCommentLine(path, strings.TrimSpace(strings.TrimPrefix(placeholderMarker, "//")))
	if strings.Contains(text, marker) {
		return nil
	}
	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	now := time.Now().Format(time.RFC3339)
	block := fmt.Sprintf("%s\n%s\n", marker, fixCommentLine(path, "Added by iflint on "+now))
	text += block
	return writeFixFile(path, []byte(text), mode)
}

func ensureLabelPlaceholder(path, label string) error {
	dirs, err := (parse.Provider{}).Parse(path)
	if os.IsNotExist(err) {
		return ensureLabelBlock(path, label)
	}
	if err != nil {
		return err
	}
	region, ok := computeLabelRanges(dirs)[label]
	if !ok {
		return ensureLabelBlock(path, label)
	}
	text, mode, err := readFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(text, "\n")
	marker := fixCommentLine(path, strings.TrimSpace(strings.TrimPrefix(placeholderMarker, "//")))
	for i := region.StartLine - 1; i < region.EndLine && i < len(lines); i++ {
		if lines[i] == marker {
			return nil
		}
	}
	index := region.EndLine
	if index < 0 || index >= len(lines) {
		return fmt.Errorf("invalid label range")
	}
	lines = append(lines[:index], append([]string{marker}, lines[index:]...)...)
	return writeFixFile(path, []byte(strings.Join(lines, "\n")), mode)
}

func ensureLabelBlock(path, label string) error {
	if path == "" || label == "" {
		return nil
	}
	syn := core.CurrentDirectiveSyntax()
	labelDecl := fixCommentLine(path, fmt.Sprintf("%s.Label(%q)", syn.Prefix, label))
	endDecl := fixCommentLine(path, fmt.Sprintf("%s.EndLabel", syn.Prefix))

	text, mode, err := readFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(text, labelDecl) {
		return nil
	}

	if len(text) > 0 && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	block := []string{
		labelDecl,
		fixCommentLine(path, strings.TrimSpace(strings.TrimPrefix(placeholderMarker, "//"))),
		endDecl,
	}
	text += strings.Join(block, "\n") + "\n"
	return writeFixFile(path, []byte(text), mode)
}

func labelExists(path, label string) bool {
	syn := core.CurrentDirectiveSyntax()
	labelDecl := fixCommentLine(path, fmt.Sprintf("%s.Label(%q)", syn.Prefix, label))
	data, _, err := readFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(data, labelDecl)
}

func readFile(path string) (string, os.FileMode, error) {
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", 0, err
			}
			return "", 0o644, nil
		}
		return "", 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return string(data), info.Mode(), nil
}

// validateFixPath limits directive-driven writes to the current workspace and rejects
// symlinks so a target cannot redirect a fix into another file tree.
func validateFixPath(target string) error { return ValidateFixPath(".", target) }

// ValidateFixPath rejects directive-driven writes outside root or through symlinks.
func ValidateFixPath(root, target string) error {
	if strings.Contains(target, "://") {
		return fmt.Errorf("refusing to fix remote target %q", target)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	// Resolve system ancestors (e.g. /var -> /private/var on macOS), but reject
	// symlinks encountered at or below the workspace boundary.
	ancestor := abs
	var suffix []string
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = filepath.Dir(ancestor)
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	canonical := resolved
	for i := len(suffix) - 1; i >= 0; i-- {
		canonical = filepath.Join(canonical, suffix[i])
	}
	// Checking both the canonical target and original descendants catches external links.
	if strings.HasPrefix(abs, root+string(filepath.Separator)) {
		originalRel, _ := filepath.Rel(root, abs)
		current := root
		for _, part := range strings.Split(originalRel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			if info, err := os.Lstat(current); err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to fix symlink target: %q", target)
			}
		}
	}
	abs = canonical
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to fix target outside workspace: %q", target)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to fix symlink target: %q", target)
		}
	}
	return nil
}

func fixCommentLine(path, text string) string { return comments.FormatComment(path, text) }

func fixPathOnly(f core.Finding) (string, bool) {
	if f.TargetPath != "" {
		return f.TargetPath, true
	}
	return extractPathOnly(f.Message)
}
func fixPathAndLabel(f core.Finding) (string, string, bool) {
	if f.TargetPath != "" && f.TargetLabel != "" {
		return f.TargetPath, f.TargetLabel, true
	}
	return extractPathAndLabel(f.Message)
}
func fixLabelNotFound(f core.Finding) (string, string, bool) {
	if f.TargetPath != "" && f.TargetLabel != "" {
		return f.TargetLabel, f.TargetPath, true
	}
	return extractLabelNotFound(f.Message)
}

func writeFixFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".iflint-fix-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(mode.Perm()); err != nil {
		file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ensureTargetPlaceholder(path, label string) error {
	if label != "" {
		return ensureLabelPlaceholder(path, label)
	}
	return ensureFilePlaceholder(path)
}
