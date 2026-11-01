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
	// ProjectReposMaxBytes is what the project step reads of the other
	// repositories' summaries.
	ProjectReposMaxBytes = 80_000
	// LibrariesMaxBytes is what a step reads of the summaries of the
	// libraries its repository, or its project, uses: beside every step,
	// so less than the project's own repositories get.
	LibrariesMaxBytes = 24_000
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
// a tool's cache: never read, wherever they sit in the tree.
var blockedDirs = map[string]bool{
	"vendor": true, "node_modules": true, "third_party": true, ".git": true, ".idea": true, ".vscode": true,
	"__pycache__": true, ".gradle": true, ".next": true, "bower_components": true,
	// Bundles and reports, never source: packages/*/dist of a monorepo and
	// a committed action's dist/ included.
	"dist": true, "coverage": true,
}

// blockedRootDirs are build output by convention at the repository's root
// only: deeper down, internal/build or cmd/target is a package like any.
var blockedRootDirs = map[string]bool{"build": true, "target": true, "out": true}

// blockedNames are lockfiles the .lock extension does not catch: long,
// generated and without a fact in them.
// Below them, key material and credential stores: a body scan would leave
// most of them out anyway, the name says it before any byte is read.
var blockedNames = map[string]bool{
	"package-lock.json": true, "pnpm-lock.yaml": true, "go.sum": true, "gradle.lockfile": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true, ".netrc": true, ".pgpass": true,
	"credentials": true, ".htpasswd": true,
}

// blockedExts are binary, media, archive and font files, compiled or
// minified output, and key stores.
var blockedExts = map[string]bool{
	".pem": true, ".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true, ".kdbx": true,
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
	if blockedRootDirs[dirs[0]] {
		return true
	}
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

// Exists is a tree as a lookup a statement's files are checked against:
// the whole tree, not the readable part (a statement may rest on a lockfile
// or a large one), with a path forgiven its "./" or "/" and a directory
// present while any file under it is.
func Exists(entries []gitrepo.Entry) func(string) bool {
	files := make(map[string]bool, len(entries))
	dirs := map[string]bool{}
	for _, e := range entries {
		if e.Deleted {
			continue
		}
		files[e.Path] = true
		for d := path.Dir(e.Path); d != "."; d = path.Dir(d) {
			if dirs[d] {
				break
			}
			dirs[d] = true
		}
	}
	return func(p string) bool {
		p = normPath(p)
		return p != "" && (files[p] || dirs[p])
	}
}

// normPath is a path as a statement may carry it, made comparable to the
// tree's: no surrounding space, no "./" or "/" in front, no "/" behind.
func normPath(p string) string {
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(p), "./"), "/"), "/")
}

// Binary reports whether content read as a file is no text: a NUL byte in
// its first 8000 bytes, git's own test.
func Binary(body []byte) bool {
	return bytes.IndexByte(body[:min(len(body), 8000)], 0) >= 0
}
