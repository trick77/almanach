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
	"regexp"
	"sort"
	"strings"

	"github.com/trick77/almanach/internal/gitrepo"
)

// Budgets of a run that are code's, not configuration: they follow from the
// model's context, not from the operator's purse.
const (
	// FileMaxBytes: what a step reads of one file at most; a larger one is
	// read as its head (File.Cut), up to CutMaxBytes. Larger is often
	// generated, vendored or data, which the name filters catch; and when
	// it is the one class the repository turns on, its head is better than
	// nothing.
	FileMaxBytes = 100_000
	// CutMaxBytes: a file over it is no source at all (a dump, a database,
	// a dataset the name filters missed) and is refused by its size before
	// a byte is read, as every large file once was: a head is cut from the
	// whole file, read and redacted first.
	CutMaxBytes = 2_000_000
	// StepMaxBytes is the file content one step shows the model: about 70k
	// tokens, which with a full page beside it fits a 128k context.
	StepMaxBytes = 240_000
	// DocsMaxBytes is what the overview step reads of the docs.
	DocsMaxBytes = 160_000
	// ProjectReposMaxBytes is what the project step reads of the other
	// repositories' summaries.
	ProjectReposMaxBytes = 80_000
	// SharedMaxBytes is what a step reads of the summaries of the shared
	// repositories its repository, or its project, depends on: beside every step,
	// so less than the project's own repositories get.
	SharedMaxBytes = 24_000
	// ContractsMaxBytes is what the project step reads of the other
	// repositories' interfaces and entities, a budget of its own beside
	// their summaries': in a large project the lists would be cut first.
	ContractsMaxBytes = 40_000
	// SiblingsMaxBytes is what a describe step reads of the names of the
	// interfaces and entities the project's other repositories offer:
	// beside every step, names only.
	SiblingsMaxBytes = 8_000
	// CatalogueDocsBytes is what the catalogue step reads of the docs.
	CatalogueDocsBytes = 60_000
	// ListMaxEntries: over it the catalogue sees the tree folded to that
	// many lines, opened where the files are (Listing).
	ListMaxEntries = 4000
)

// File is a path at the run's commit and the bytes of it a step reads: the
// whole file, or, Cut, its first FileMaxBytes (Size is then FileMaxBytes).
// A file over the cap was once left out whole, and the god class at the
// centre of a repository is exactly such a file: its head says what it is,
// its tail seldom more.
type File struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Cut  bool   `json:"cut,omitempty"`
}

// blockedDirs are directory names whose content is someone else's code or
// a tool's cache: never read, wherever they sit in the tree.
var blockedDirs = map[string]bool{
	"vendor": true, "node_modules": true, "third_party": true, ".git": true, ".idea": true, ".vscode": true,
	"__pycache__": true, ".gradle": true, ".next": true, "bower_components": true,
	// Bundles and reports, never source: packages/*/dist of a monorepo and
	// a committed action's dist/ included.
	"dist": true, "coverage": true,
	// Generated sources and a generator's own state: a committed API client
	// read as code outweighs the app it serves.
	"generated": true, "generated-sources": true, "generated-test-sources": true, "__generated__": true,
	".openapi-generator": true,
	// IDE metadata: Eclipse's and IntelliJ's run configurations.
	".settings": true, ".run": true,
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
	// IDE and generator metadata.
	".classpath": true, ".project": true, ".factorypath": true, ".openapi-generator-ignore": true,
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

// BriefPath is where Almanach's own brief of a repository lands in it (the
// worker's brief push): never read back, or a page would rest on its own
// earlier words as if they were the code's.
const BriefPath = ".agents/PROJECT.md"

// Blocked reports whether a path is never read: vendored, built, locked,
// binary or generated by its name alone, or Almanach's own brief.
func Blocked(p string) bool {
	if p == BriefPath {
		return true
	}
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
// or executable blob, not empty, within CutMaxBytes, not Blocked. One over
// FileMaxBytes is read as its head (Cut).
func Readable(e gitrepo.Entry) bool {
	return !e.Deleted && gitrepo.Indexable(e.Mode) && e.Size > 0 && e.Size <= CutMaxBytes && !Blocked(e.Path)
}

// Files is the readable part of a tree, by path, a file over FileMaxBytes
// as the head of it. Each kind of tests is left out unless it is most of
// it (kindOf, keepTests): the catalogue was asked to leave them out and
// did not, and a step's budget went to them. Schema migrations are left
// out (MigrationFile).
func Files(entries []gitrepo.Entry) []File {
	var all []File
	kinds := map[string]testKind{}
	count := map[testKind]int{}
	for _, e := range entries {
		if Readable(e) && !MigrationFile(e.Path) {
			f := FileOf(e)
			k := kindOf(f.Path)
			kinds[f.Path] = k
			count[k]++
			all = append(all, f)
		}
	}
	out := []File{}
	for _, f := range all {
		if k := kinds[f.Path]; k == noTest || keepTests(count[k], len(all)) {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// FileOf is a readable entry as a step reads it: whole within FileMaxBytes,
// else its first FileMaxBytes, Cut.
func FileOf(e gitrepo.Entry) File {
	if e.Size > FileMaxBytes {
		return File{Path: e.Path, Size: FileMaxBytes, Cut: true}
	}
	return File{Path: e.Path, Size: e.Size}
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

// generatedMark is what a generator writes at the top of its output. A bare
// @Generated is left alone: hand-written code wears it to stay out of
// coverage; a generator's carries its name in parentheses.
var generatedMark = regexp.MustCompile(`(?i)code generated .{0,200}do not edit|<auto-generated|` +
	`generated by (the )?(openapi|swagger|jaxb|wsimport|xjc|protoc|protocol buffer compiler)|` +
	`@(javax\.annotation\.(processing\.)?|jakarta\.annotation\.)?generated\(`)

// generatedSay is a generator's phrase hand-written prose uses too ("the
// key is auto-generated by the database"): a mark only beside
// generatedWarn, the warning a generator puts with it.
var (
	generatedSay  = regexp.MustCompile(`(?i)this (file|class|code) (was|is) (automatically |auto[- ]?)?generated|auto[- ]?generated by`)
	generatedWarn = regexp.MustCompile(`(?i)do not (edit|modify)|will be lost|edit the class manually`)
)

// Generated reports whether content read as a file is a generator's output
// by its head (first 4 kB): committed generated sources the names miss
// (Blocked), read and described as if written by hand, never again.
func Generated(body []byte) bool {
	head := body[:min(len(body), 4096)]
	return generatedMark.Match(head) || generatedSay.Match(head) && generatedWarn.Match(head)
}
