package sourcetree

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ignoreRule is one line of a .gitignore file.
type ignoreRule struct {
	base     string // directory holding the .gitignore, relative to the walk root ("" for root or above)
	above    string // for a .gitignore above the walk root: the path from its directory down to the root
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

// ancestorRules loads the .gitignore files of the directories above root, up
// to and including the git repository root (the nearest ancestor holding
// .git), outermost first, as git applies them to a subfolder. A tree outside
// any repository has none. The global excludes file and .git/info/exclude
// are host-specific and ignored on purpose so every host selects the same
// files.
func ancestorRules(root string) ([]ignoreRule, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var dirs []string // innermost first
	for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
		dirs = append(dirs, dir)
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		if filepath.Dir(dir) == dir {
			return nil, nil // not in a git repository
		}
	}
	var rules []ignoreRule
	for i := len(dirs) - 1; i >= 0; i-- {
		own, err := loadGitignore(dirs[i], "")
		if err != nil {
			return nil, err
		}
		below, err := filepath.Rel(dirs[i], abs)
		if err != nil {
			return nil, err
		}
		for _, r := range own {
			r.above = filepath.ToSlash(below)
			rules = append(rules, r)
		}
	}
	return rules, nil
}

// loadGitignore parses <root>/<dirRel>/.gitignore. A missing file yields no
// rules.
func loadGitignore(root, dirRel string) ([]ignoreRule, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(dirRel), ".gitignore"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var rules []ignoreRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		line = trimTrailingSpaces(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := ignoreRule{base: dirRel}
		if strings.HasPrefix(line, "!") {
			r.negate = true
			line = line[1:]
		} else if strings.HasPrefix(line, `\`) {
			line = line[1:] // "\#" or "\!" escapes
		}
		if strings.HasSuffix(line, "/") {
			r.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if strings.Contains(line, "/") {
			r.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		r.pattern = line
		rules = append(rules, r)
	}
	return rules, sc.Err()
}

func trimTrailingSpaces(s string) string {
	for strings.HasSuffix(s, " ") && !strings.HasSuffix(s, `\ `) {
		s = s[:len(s)-1]
	}
	return s
}

// ignored applies rules in order (outer .gitignore files first); the last
// matching rule wins, as in git.
func ignored(rules []ignoreRule, rel string, isDir bool) bool {
	result := false
	for _, r := range rules {
		if r.dirOnly && !isDir {
			continue
		}
		sub := rel
		if r.above != "" {
			sub = r.above + "/" + rel
		} else if r.base != "" {
			if !strings.HasPrefix(rel, r.base+"/") {
				continue
			}
			sub = strings.TrimPrefix(rel, r.base+"/")
		}
		var ok bool
		if r.anchored {
			ok = matchPath(r.pattern, sub)
		} else {
			m, err := path.Match(r.pattern, path.Base(sub))
			ok = err == nil && m
		}
		if ok {
			result = !r.negate
		}
	}
	return result
}
