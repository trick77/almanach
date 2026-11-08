package gitrepo

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// Churn is how many commits reachable from sha, committed since since,
// touched each path: a router's hint where the code moves, never a cut.
// Merges are left out (their changes are their parents'), and so are the
// commits of a bot and those that only bump a dependency: churn would
// otherwise reward pom.xml and lock files, which Renovate touches weekly.
func (c *Client) Churn(ctx context.Context, dir, sha string, since time.Time) (map[string]int, error) {
	if err := checkSHA(sha); err != nil {
		return nil, err
	}
	// Each commit: a record separator, author name, email and subject split
	// by unit separators and ended by NUL, then the paths it touched, each
	// NUL-terminated (-z): a path is named as the tree names it, quotes,
	// tabs and newlines included.
	out, err := c.run(ctx, dir, inRepo(dir), "log", "-z", "--no-merges", "--no-renames", "--name-only",
		"--since="+since.UTC().Format(time.RFC3339), "--format=%x1e%an%x1f%ae%x1f%s", sha, "--")
	if err != nil {
		return nil, err
	}
	return countChurn(out), nil
}

// reBumpSubject is a dependency bump's subject, as Renovate, Dependabot and
// the conventional "deps" scope write it: "Bump x from 1 to 2", "Update
// dependency x to v2", "Update x to v1.2.3" and nothing after. Never a
// human's "Update retry delay to 5 seconds" or "Bump batch size".
var reBumpSubject = regexp.MustCompile(`(?i)^(\w+\(deps(-dev)?\)!?:|deps(-dev)?:|bump\s+\S+\s+from\s+\S+\s+to\s|update\s+(dependency|module|plugin)\s|update\s+\S+\s+to\s+v?\d[\w.\-+]*\s*$|chore\(renovate\)|lock file maintenance)`)

// bot reports whether an author is an automation: a GitHub App's "[bot]",
// Renovate, Dependabot, or an address naming a bot.
func bot(name, email string) bool {
	n, e := strings.ToLower(name), strings.ToLower(email)
	return strings.Contains(n, "[bot]") || strings.Contains(n, "renovate") || strings.Contains(n, "dependabot") ||
		strings.Contains(e, "[bot]") || strings.Contains(e, "renovate") || strings.Contains(e, "dependabot") ||
		strings.HasPrefix(e, "bot@") || strings.Contains(e, "-bot@") || strings.Contains(e, ".bot@")
}

// countChurn counts git log's records (Churn's format) per path.
func countChurn(out string) map[string]int {
	churn := map[string]int{}
	for _, rec := range strings.Split(out, "\x1e") {
		head, files, _ := strings.Cut(rec, "\x00")
		parts := strings.SplitN(head, "\x1f", 3)
		if len(parts) != 3 || bot(parts[0], parts[1]) || reBumpSubject.MatchString(strings.TrimSpace(parts[2])) {
			continue
		}
		// The paths follow the header's NUL after one newline, each ended
		// by NUL; a path is never trimmed, its spaces are its own.
		for _, f := range strings.Split(strings.TrimPrefix(files, "\n"), "\x00") {
			if f != "" {
				churn[f]++
			}
		}
	}
	return churn
}
