// Package baseline is what a baseline run decides without I/O: which files
// of a tree are worth reading, which are the docs, how the catalogue's
// answer becomes a plan within budget, what a reconcile must read again,
// and how a step's patch applies to a page without erasing what the PRs
// taught. The worker does the reading and the calling; this package only
// decides, so every rule here is a table test.
package baseline

import (
	"bytes"
	"path"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/gitrepo"
)

// Budgets of a run that are code's, not configuration: they follow from the
// model's context, not from the operator's purse.
const (
	// FileMaxBytes: a file over it is refused by its size, never read. Larger
	// is generated, vendored or data.
	FileMaxBytes = 100_000
	// StepMaxBytes is the file content one step shows the model: about 70k
	// tokens, which with a full page beside it fits a 128k context.
	StepMaxBytes = 240_000
	// DocsMaxBytes is what the overview step reads of the docs.
	DocsMaxBytes = 160_000
	// CatalogueDocsBytes is what the catalogue step reads of the docs.
	CatalogueDocsBytes = 60_000
	// ListMaxEntries: over it the catalogue sees directories, not files.
	ListMaxEntries = 4000
)

// File is a path at the run's commit and its size.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// blockedDirs are directory names whose content is someone else's code or
// build output: never read, wherever they sit in the tree.
var blockedDirs = map[string]bool{
	"vendor": true, "node_modules": true, "third_party": true, "dist": true, "build": true, "target": true,
	".git": true, ".idea": true, ".vscode": true, "__pycache__": true, "coverage": true, ".gradle": true,
	".next": true, "bower_components": true,
}

// blockedNames are lockfiles: long, generated and without a fact in them.
var blockedNames = map[string]bool{
	"package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true, "go.sum": true, "Cargo.lock": true,
	"poetry.lock": true, "composer.lock": true, "Gemfile.lock": true, "Pipfile.lock": true, "gradle.lockfile": true,
}

// blockedExts are binary, media, archive and font files, and compiled or
// minified output.
var blockedExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true, ".webp": true, ".bmp": true, ".svg": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".jar": true,
	".war": true, ".ear": true, ".class": true, ".so": true, ".dylib": true, ".dll": true, ".exe": true, ".bin": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true, ".mp3": true, ".mp4": true, ".mov": true,
	".wav": true, ".map": true, ".snap": true, ".lock": true, ".pyc": true, ".o": true, ".a": true,
}

// blockedSuffixes are generated sources by naming convention.
var blockedSuffixes = []string{".min.js", ".min.css", ".pb.go", ".pb.gw.go", "_pb2.py", ".g.dart"}

// Blocked reports whether a path is never read: vendored, built, locked,
// binary or generated, by its name alone.
func Blocked(p string) bool {
	dirs := strings.Split(path.Dir(p), "/")
	for _, d := range dirs {
		if blockedDirs[d] {
			return true
		}
	}
	base := path.Base(p)
	if blockedNames[base] || blockedExts[strings.ToLower(path.Ext(base))] {
		return true
	}
	lower := strings.ToLower(base)
	for _, s := range blockedSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return strings.Contains(lower, "_generated.") || strings.Contains(lower, ".generated.")
}

// Readable reports whether a tree entry is a file a run may read: a regular
// or executable blob, not empty, within FileMaxBytes, not Blocked.
func Readable(e gitrepo.Entry) bool {
	return !e.Deleted && gitrepo.Indexable(e.Mode) && e.Size > 0 && e.Size <= FileMaxBytes && !Blocked(e.Path)
}

// Files is the readable part of a tree, by path.
func Files(entries []gitrepo.Entry) []File {
	out := []File{}
	for _, e := range entries {
		if Readable(e) {
			out = append(out, File{Path: e.Path, Size: e.Size})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Binary reports whether content read as a file is no text: a NUL byte in
// its first 8000 bytes, git's own test.
func Binary(body []byte) bool {
	return bytes.IndexByte(body[:min(len(body), 8000)], 0) >= 0
}
