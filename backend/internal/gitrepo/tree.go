package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The reads a baseline run makes of a clone, all at a commit of the object
// store, after ../rongo's package: the tree with modes and sizes, what
// changed between two commits, ancestry, a commit's time. core.quotePath is
// off everywhere: by default git C-quotes any path with a non-ASCII byte,
// and the quoted form is no path, so every umlaut file would drop out.

// Entry is one path at a commit (or in a diff, at the newer commit).
type Entry struct {
	Path string
	// Deleted: a diff entry the newer commit no longer has.
	Deleted bool
	// Mode tells a symlink (120000) or a submodule pointer (160000) from a
	// file before it is read; "" when deleted.
	Mode string
	// Size in bytes, 0 when deleted or not a file: what lets a reader refuse
	// a file over its cap without reading it.
	Size int64
}

// Indexable reports whether a tree entry of this mode is a file to read:
// regular and executable blobs, never a symlink or a submodule pointer.
func Indexable(mode string) bool {
	return mode == "100644" || mode == "100755"
}

// ListEntries lists every tracked entry at a commit with its mode and size.
func (c *Client) ListEntries(ctx context.Context, dir, sha string) ([]Entry, error) {
	out, err := c.run(ctx, dir, nil, "-c", "core.quotePath=false", "ls-tree", "-z", "-r", "-l", sha)
	if err != nil {
		return nil, err
	}
	entries := []Entry{}
	for _, line := range nulFields(out) {
		// <mode> <type> <sha> <size>\t<path>; the size is "-" for a
		// submodule pointer, which names a commit and has none.
		meta, path, ok := strings.Cut(line, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 4 {
			return nil, fmt.Errorf("unparseable tree record %q", line)
		}
		size, _ := strconv.ParseInt(fields[3], 10, 64)
		entries = append(entries, Entry{Path: path, Mode: fields[0], Size: size})
	}
	return entries, nil
}

// ChangedEntries is what differs between two commits, each path with
// whether the newer commit still has it, its mode and its size there.
// --no-renames: a rename is a delete and an add, so the old path is named
// too. --raw carries the modes; the sizes come from one cat-file by the
// full object id (an abbreviation is a revision to cat-file, where a ref of
// that name would win).
func (c *Client) ChangedEntries(ctx context.Context, dir, fromSHA, toSHA string) ([]Entry, error) {
	out, err := c.run(ctx, dir, nil, "-c", "core.quotePath=false",
		"diff", "-z", "--raw", "--no-renames", "--no-abbrev", fromSHA+".."+toSHA)
	if err != nil {
		return nil, err
	}
	// -z: each record is its metadata and its path as two NUL-terminated
	// fields, the path verbatim.
	recs := nulFields(out)
	if len(recs)%2 != 0 {
		return nil, fmt.Errorf("unparseable diff output: %d fields", len(recs))
	}
	changes := []Entry{}
	var blobs []int
	var oids strings.Builder
	for i := 0; i < len(recs); i += 2 {
		// :<src mode> <dst mode> <src sha> <dst sha> <status>, then the path
		fields := strings.Fields(strings.TrimPrefix(recs[i], ":"))
		if len(fields) != 5 {
			return nil, fmt.Errorf("unparseable diff record %q", recs[i])
		}
		ch := Entry{Path: recs[i+1], Deleted: strings.HasPrefix(fields[4], "D")}
		if !ch.Deleted {
			ch.Mode = fields[1]
		}
		if Indexable(ch.Mode) {
			blobs = append(blobs, len(changes))
			oids.WriteString(fields[3] + "\n")
		}
		changes = append(changes, ch)
	}
	if len(blobs) == 0 {
		return changes, nil
	}
	sized, err := c.runIn(ctx, dir, strings.NewReader(oids.String()), nil, "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	lines := nonEmptyLines(string(sized))
	if len(lines) != len(blobs) {
		return nil, fmt.Errorf("sized %d of %d changed blobs", len(lines), len(blobs))
	}
	for i, line := range lines {
		_, size, err := parseBatchHeader(line)
		if err != nil {
			return nil, err
		}
		changes[blobs[i]].Size = size
	}
	return changes, nil
}

// IsAncestor reports whether ancestor is reachable from descendant. Exit 1
// is "no"; anything else (128: a missing object) is an error the caller
// must not read as "diverged".
func (c *Client) IsAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	_, err := c.run(ctx, dir, nil, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// HasCommit reports whether the clone holds sha as a commit: a documented
// commit of a rewritten branch may be gone after a prune.
func (c *Client) HasCommit(ctx context.Context, dir, sha string) (bool, error) {
	_, err := c.run(ctx, dir, nil, "cat-file", "-e", sha+"^{commit}")
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 128 || exitCode(err) == 1 {
		// "Not a valid object name": absent, not broken. A clone that
		// cannot be read at all fails the next command loudly.
		if _, checkErr := c.run(ctx, dir, nil, "rev-parse", "--git-dir"); checkErr != nil {
			return false, fmt.Errorf("repository unreadable: %w", checkErr)
		}
		return false, nil
	}
	return false, err
}

// CommitTime is a commit's committer time, UTC.
func (c *Client) CommitTime(ctx context.Context, dir, sha string) (time.Time, error) {
	out, err := c.run(ctx, dir, nil, "show", "-s", "--format=%ct", sha+"^{commit}")
	if err != nil {
		return time.Time{}, err
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("commit time of %s: %q", ShortSHA(sha), strings.TrimSpace(out))
	}
	return time.Unix(secs, 0).UTC(), nil
}

// errNoObject is git saying the request names nothing it can read.
var errNoObject = errors.New("no such object")

// parseBatchHeader reads the line cat-file answers a request with, in
// --batch and --batch-check alike: "<oid> <type> <size>" for an object,
// "<request> missing" (or "ambiguous") for anything else. A request may
// itself hold spaces, so a miss is told by its last word and an object by
// having exactly three fields.
func parseBatchHeader(line string) (kind string, size int64, err error) {
	if strings.HasSuffix(line, " missing") || strings.HasSuffix(line, " ambiguous") {
		return "", 0, fmt.Errorf("%w: %s", errNoObject, line)
	}
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", 0, fmt.Errorf("unparseable answer %q", line)
	}
	size, err = strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return "", 0, fmt.Errorf("unparseable size in %q", line)
	}
	return fields[1], size, nil
}

// nulFields splits -z output into its fields, verbatim: a path may end in a
// space, so nothing is trimmed.
func nulFields(s string) []string { return splitNonEmpty(s, "\x00") }

// nonEmptyLines splits s into its lines, verbatim.
func nonEmptyLines(s string) []string { return splitNonEmpty(s, "\n") }

func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, f := range strings.Split(s, sep) {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
