// Package sourcetree selects the source files under a root directory that
// a build copies and the fingerprinter hashes, applying include globs,
// exclude globs and .gitignore files. Excluded directories are pruned
// without being read.
package sourcetree

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// File is one selected file.
type File struct {
	// Rel is the path relative to the root, with "/" separators.
	Rel string
	// Abs is the path on disk.
	Abs string
}

// Select walks root and returns regular files whose relative path matches
// any include glob and that are neither excluded nor .gitignored, sorted by
// Rel in byte order. Symlinks to files are followed; symlinked directories
// are not descended into.
func Select(root string, include, exclude []string) ([]File, error) {
	var files []File
	rulesByDir := map[string][]ignoreRule{}
	above, err := ancestorRules(root)
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			parent := ""
			if rel != "." {
				parent = parentDir(rel)
				if isExcluded(exclude, rulesByDir[parent], rel, true) {
					return filepath.SkipDir
				}
			} else {
				rel = ""
			}
			own, err := loadGitignore(root, rel)
			if err != nil {
				return err
			}
			inherited := rulesByDir[parent]
			if rel == "" {
				inherited = above
			}
			rulesByDir[rel] = append(append([]ignoreRule(nil), inherited...), own...)
			return nil
		}

		if isExcluded(exclude, rulesByDir[parentDir(rel)], rel, false) {
			return nil
		}
		if !matchesAny(include, rel) {
			return nil
		}
		info, err := os.Stat(p) // follows symlinks
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, File{Rel: rel, Abs: p})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Rel < files[j].Rel })
	return files, nil
}

func parentDir(rel string) string {
	for i := len(rel) - 1; i >= 0; i-- {
		if rel[i] == '/' {
			return rel[:i]
		}
	}
	return ""
}

func isExcluded(exclude []string, rules []ignoreRule, rel string, isDir bool) bool {
	for _, pat := range exclude {
		if matchExclude(pat, rel) {
			return true
		}
	}
	return ignored(rules, rel, isDir)
}

func matchesAny(globs []string, rel string) bool {
	for _, g := range globs {
		if matchPath(g, rel) {
			return true
		}
	}
	return false
}
