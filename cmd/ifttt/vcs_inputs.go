package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func fatalInput(err error) { fmt.Fprintln(os.Stderr, "ifttt:", err); os.Exit(2) }

// Legacy patch files remain distinct from structural source selections. Explicit
// --files accepts even files named .diff and removes positional ambiguity.
func classifyInputs(args, explicit []string, revision bool) ([]string, string, error) {
	selected := append([]string(nil), explicit...)
	patch := ""
	for _, arg := range args {
		ext := strings.ToLower(filepath.Ext(arg))
		if !revision && (arg == "-" || ext == ".diff" || ext == ".patch" || looksLikePatch(arg)) {
			if patch != "" || len(selected) > 0 {
				return nil, "", errors.New("supply one patch file or structural source files")
			}
			patch = arg
		} else {
			if patch != "" {
				return nil, "", errors.New("cannot mix a patch file and structural source files")
			}
			selected = append(selected, arg)
		}
	}
	return selected, patch, nil
}
func rootSelectedFiles(files []string, root string) ([]string, error) {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(files))
	for _, file := range files {
		absolute := filepath.Join(cwd, file)
		if filepath.IsAbs(file) {
			input := file
			suffix := ""
			if index := strings.IndexAny(file, "*?["); index >= 0 {
				input = filepath.Dir(file[:index])
				suffix, err = filepath.Rel(input, file)
				if err != nil {
					return nil, err
				}
			}
			absolute, err = canonicalInputPath(input)
			if err != nil {
				return nil, err
			}
			if suffix != "" {
				absolute = filepath.Join(absolute, suffix)
			}
		}
		relative, err := filepath.Rel(root, absolute)
		if err != nil {
			return nil, err
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("selected file %q is outside repository", file)
		}
		result = append(result, relative)
	}
	return result, nil
}

// Resolve filesystem aliases even when the leaf is a new scaffold.
func canonicalInputPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(ancestor))
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		ancestor = parent
	}
}
func expandSelectedFiles(patterns []string) ([]string, error) {
	return expandTrackedFiles(patterns, nil)
}
func expandTrackedFiles(patterns, tracked []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, pattern := range patterns {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return nil, fmt.Errorf("invalid file glob %q: %w", pattern, err)
		}
		matches := []string{}
		isGlob := strings.ContainsAny(pattern, "*?[")
		if isGlob {
			parts := strings.Split(strings.TrimPrefix(filepath.ToSlash(pattern), "./"), "/")
			candidates := tracked
			if candidates == nil {
				walkRoot := "."
				if index := strings.IndexAny(pattern, "*?["); index > 0 {
					prefix := pattern[:index]
					if strings.HasSuffix(prefix, string(filepath.Separator)) {
						walkRoot = filepath.Clean(prefix)
					} else {
						walkRoot = filepath.Dir(prefix)
					}
				}
				err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						if d.Name() == ".git" || d.Name() == ".jj" {
							return filepath.SkipDir
						}
						return nil
					}
					if d.Type().IsRegular() {
						candidates = append(candidates, path)
					}
					return nil
				})
				if err != nil {
					return nil, err
				}
			}
			for _, candidate := range candidates {
				if matchFileGlobParts(parts, filepath.ToSlash(candidate)) {
					info, e := os.Stat(candidate)
					if errors.Is(e, fs.ErrNotExist) {
						continue
					}
					if e != nil {
						return nil, e
					}
					if !info.Mode().IsRegular() {
						continue
					}
					matches = append(matches, candidate)
				}
			}
		} else {
			matches = []string{pattern}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("selected file/glob %q matched no files", pattern)
		}
		for _, file := range matches {
			// Glob matches have already passed the same regular-file check.
			// Explicit literals still need their status/error validation here.
			if !isGlob {
				info, err := os.Stat(file)
				if err != nil {
					return nil, err
				}
				if !info.Mode().IsRegular() {
					return nil, fmt.Errorf("selected file %q is not a regular file", file)
				}
			}
			file = filepath.Clean(file)
			if !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	sort.Strings(files)
	return files, nil
}
func matchFileGlob(pattern, path string) bool {
	p := strings.Split(strings.TrimPrefix(pattern, "./"), "/")
	return matchFileGlobParts(p, path)
}

func matchFileGlobParts(p []string, path string) bool {
	f := strings.Split(strings.TrimPrefix(path, "./"), "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(p) {
			return j == len(f)
		}
		if p[i] == "**" {
			return match(i+1, j) || (j < len(f) && match(i, j+1))
		}
		if j == len(f) {
			return false
		}
		ok, _ := filepath.Match(p[i], f[j])
		return ok && match(i+1, j+1)
	}
	return match(0, 0)
}
func hasSuppression(messages string) bool {
	for _, line := range strings.Split(messages, "\n") {
		if strings.HasPrefix(line, "NO_IFTTT=") {
			fmt.Fprintf(os.Stderr, "ifttt: co-change checks suppressed: %s\n", strings.TrimPrefix(line, "NO_IFTTT="))
			return true
		}
	}
	return false
}

func looksLikePatch(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 4096)
	line, _, err := reader.ReadLine()
	if err != nil {
		return false
	}
	return strings.HasPrefix(string(line), "diff --git ") || strings.HasPrefix(string(line), "--- ") || strings.HasPrefix(string(line), "diff --cc ")
}

// Standard flag parsing stops at the first filename. Reorder registered options
// while keeping each value attached and honoring -- for literal filenames.
func reorderFlagArguments(flags *flag.FlagSet, args []string) []string {
	var options, files []string
	literal := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if literal {
			files = append(files, arg)
			continue
		}
		if arg == "--" {
			literal = true
			continue
		}
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			files = append(files, arg)
			continue
		}
		options = append(options, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		entry := flags.Lookup(name)
		if entry == nil {
			continue
		}
		if boolean, ok := entry.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			options = append(options, args[i])
		}
	}
	if len(files) > 0 {
		options = append(options, "--")
		options = append(options, files...)
	}
	return options
}
