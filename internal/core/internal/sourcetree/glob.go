package sourcetree

import (
	"path"
	"strings"
)

// matchPath reports whether the slash-separated relative path rel matches
// pattern. Pattern segments are matched with path.Match; a "**" segment
// matches zero or more whole segments.
func matchPath(pattern, rel string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(rel, "/"))
}

func matchSegments(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse consecutive "**" and try every split point.
			for len(pat) > 0 && pat[0] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegments(pat, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// matchExclude reports whether rel (a file or directory) matches a
// source_exclude pattern. A pattern without "/" matches the base name at any
// depth, like .gitignore; a pattern with "/" matches the whole relative path.
func matchExclude(pattern, rel string) bool {
	pattern = strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/")
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(rel))
		return err == nil && ok
	}
	return matchPath(pattern, rel)
}
