// Package pipeline parses qdnder.yaml and expands image tag templates.
package pipeline

import (
	"strconv"
	"strings"
	"time"
)

// TagContext carries the values available to tag templates.
type TagContext struct {
	Tag    string // git tag when the build was triggered by one
	Branch string // branch name
	SHA    string // full commit sha
	Now    time.Time
}

// ExpandTags expands {tag}, {branch}, {sha}, {sha:N} and {date} templates.
// A template that resolves to an empty value drops the whole tag entry,
// so "{tag}" on a branch build simply disappears.
func ExpandTags(tags []string, c TagContext) []string {
	if c.Now.IsZero() {
		c.Now = time.Now()
	}
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		v := expandOne(t, c)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func expandOne(t string, c TagContext) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(t, '{')
		if i < 0 {
			b.WriteString(t)
			break
		}
		j := strings.IndexByte(t[i:], '}')
		if j < 0 {
			b.WriteString(t)
			break
		}
		b.WriteString(t[:i])
		key := t[i+1 : i+j]
		t = t[i+j+1:]
		switch {
		case key == "tag":
			if c.Tag == "" {
				return ""
			}
			b.WriteString(c.Tag)
		case key == "branch":
			b.WriteString(sanitizeRef(c.Branch))
		case key == "sha":
			b.WriteString(c.SHA)
		case strings.HasPrefix(key, "sha:"):
			n, err := strconv.Atoi(key[4:])
			if err != nil || n <= 0 {
				return ""
			}
			if len(c.SHA) < n {
				return ""
			}
			b.WriteString(c.SHA[:n])
		case key == "date":
			b.WriteString(c.Now.UTC().Format("20060102"))
		default:
			return "" // unknown template: drop the tag rather than guess
		}
	}
	return b.String()
}

// sanitizeRef makes a branch name safe for an image tag.
func sanitizeRef(ref string) string {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	ref = strings.TrimPrefix(ref, "refs/tags/")
	var b strings.Builder
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-.")
}

// ShortSHA returns the first 12 characters of a commit sha.
func ShortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// ExpandValue expands templates in a single value (e.g. a build arg);
// unknown or empty templates resolve to an empty string.
func ExpandValue(v string, c TagContext) string { return expandOne(v, c) }
