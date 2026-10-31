package baseline

import (
	"path"
	"sort"
	"strings"
)

// The docs a repository keeps about itself and the manifests that say what
// it builds and pulls in: read first, by code's choice and never a model's,
// so the overview does not depend on a cheap model's taste.

// docRank orders what the overview reads: the README before the agent
// notes before the architecture docs before other docs at the root, then
// the manifests, then everything under docs/, then diagram sources last, so
// a large generated one never crowds out prose. -1 is no doc.
func docRank(p string) int {
	base := strings.ToLower(path.Base(p))
	depth := strings.Count(p, "/")
	stem := strings.TrimSuffix(base, path.Ext(base))
	// A named doc is prose: design.go or Configuration.java are code.
	if !docText(base) && path.Ext(base) != "" {
		stem = ""
	}
	switch {
	case depth == 0 && stem == "readme":
		return 0
	case depth == 0 && (base == "agents.md" || base == "claude.md"):
		return 1
	case depth == 0 && (stem == "architecture" || stem == "how_it_works" || stem == "design"):
		return 2
	case depth <= 1 && (stem == "readme" || stem == "contributing" || stem == "configuration"):
		return 3
	case depth <= 1 && manifest(base):
		return 4
	case docsDir(p) && docText(base):
		return 5
	case (depth == 0 || docsDir(p)) && diagram(base):
		return 6
	}
	return -1
}

// IsDoc reports whether the overview would read a path, as a statement
// names it ("./" and "/" forgiven): a doc, a manifest, a diagram source, or
// a docs directory.
func IsDoc(p string) bool {
	p = normPath(p)
	return p == "docs" || p == "doc" || docRank(p) >= 0
}

// diagram reports whether a file is a diagram's text source. A .drawio is
// not: its XML is layout, not content.
func diagram(base string) bool {
	switch path.Ext(base) {
	case ".puml", ".plantuml", ".iuml", ".pu", ".mmd", ".mermaid":
		return true
	}
	return false
}

// manifest reports whether a file name says what a repository builds, runs
// or depends on.
func manifest(base string) bool {
	switch base {
	case "go.mod", "package.json", "pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts",
		"cargo.toml", "pyproject.toml", "requirements.txt", "setup.py", "gemfile", "composer.json",
		"compose.yaml", "compose.yml", "containerfile", "dockerfile", "makefile":
		return true
	}
	if strings.HasPrefix(base, "docker-compose") && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
		return true
	}
	return strings.HasPrefix(base, "openapi") && (strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".json"))
}

func docsDir(p string) bool {
	return strings.HasPrefix(p, "docs/") || strings.HasPrefix(p, "doc/")
}

func docText(base string) bool {
	switch path.Ext(base) {
	case ".md", ".markdown", ".mdx", ".adoc", ".asciidoc", ".rst", ".org", ".textile", ".txt":
		return true
	}
	return false
}

// Docs picks the docs and manifests among readable files, most telling
// first, until limit bytes: a file that does not fit is skipped and a smaller
// one after it may still be taken.
func Docs(files []File, limit int64) []File {
	type ranked struct {
		File
		rank, depth int
	}
	var cand []ranked
	for _, f := range files {
		if r := docRank(f.Path); r >= 0 {
			cand = append(cand, ranked{f, r, strings.Count(f.Path, "/")})
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		a, b := cand[i], cand[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.depth != b.depth {
			return a.depth < b.depth
		}
		return a.Path < b.Path
	})
	out := []File{}
	var used int64
	for _, c := range cand {
		if used+c.Size > limit {
			continue
		}
		used += c.Size
		out = append(out, c.File)
	}
	return out
}
