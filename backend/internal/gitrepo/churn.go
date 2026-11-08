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
	// Each commit: a record separator, author name, email and subject
	// split by unit separators, then the paths it touched, one a line.
	// quotePath off: a path with an umlaut is named as the tree names it.
	out, err := c.run(ctx, dir, inRepo(dir), "-c", "core.quotePath=false", "log", "--no-merges", "--no-renames", "--name-only",
		"--since="+since.UTC().Format(time.RFC3339), "--format=%x1e%an%x1f%ae%x1f%s", sha, "--")
	if err != nil {
		return nil, err
	}
	return countChurn(out), nil
}

// reBumpSubject is a dependency bump's subject, as Renovate, Dependabot and
// the conventional "deps" scope write it.
var reBumpSubject = regexp.MustCompile(`(?i)^(\w+\(deps(-dev)?\)!?:|deps(-dev)?:|bump\s|update\s+(dependency|module|plugin|.*\s+to\s+v?\d)|chore\(renovate\)|lock file maintenance)`)

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
		head, files, _ := strings.Cut(rec, "\n")
		parts := strings.SplitN(head, "\x1f", 3)
		if len(parts) != 3 || bot(parts[0], parts[1]) || reBumpSubject.MatchString(strings.TrimSpace(parts[2])) {
			continue
		}
		for _, f := range strings.Split(files, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				churn[f]++
			}
		}
	}
	return churn
}
