// Package teamkeys maps a bearer token to a team. The file is JSON,
// {"<team>": "<token>"}, mounted from a Secret and never committed; it is
// re-read when its modification time changes, so a new team needs no
// restart. The token IS the team's identity: nothing in a payload names a
// team, and a lookup compares against every entry in constant time, so
// timing reveals neither which team matched nor how many there are.
package teamkeys

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Keys is the live token table.
type Keys struct {
	path string
	mu   sync.Mutex
	mod  time.Time
	// byToken is token -> team; teams is the sorted names, for the log.
	byToken map[string]string
	teams   []string
}

// Load reads the file once. An unreadable or empty file is a startup fault.
func Load(path string) (*Keys, error) {
	k := &Keys{path: path}
	if err := k.reload(); err != nil {
		return nil, err
	}
	return k, nil
}

// Teams is the configured team names, sorted.
func (k *Keys) Teams() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.teams...)
}

// Lookup is the team a bearer belongs to, "" when none. It re-reads the
// file first when its mtime moved; a file that fails to parse keeps the
// last good table and is logged by the caller through the returned error.
func (k *Keys) Lookup(token string) (team string, reloadErr error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if st, err := os.Stat(k.path); err == nil && !st.ModTime().Equal(k.mod) {
		reloadErr = k.reload()
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return "", reloadErr
	}
	// Every entry is compared, found or not, so the time taken says nothing.
	for candidate, t := range k.byToken {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			team = t
		}
	}
	return team, reloadErr
}

// reload reads the file into the table; the caller holds the lock. A
// malformed file is refused and the old table kept.
func (k *Keys) reload() error {
	st, err := os.Stat(k.path)
	if err != nil {
		return fmt.Errorf("team keys: %w", err)
	}
	raw, err := os.ReadFile(k.path)
	if err != nil {
		return fmt.Errorf("team keys: %w", err)
	}
	var byTeam map[string]string
	if err := json.Unmarshal(raw, &byTeam); err != nil {
		return fmt.Errorf("team keys: %s: %w", k.path, err)
	}
	byToken := make(map[string]string, len(byTeam))
	var teams []string
	for team, token := range byTeam {
		team, token = strings.TrimSpace(team), strings.TrimSpace(token)
		if team == "" || token == "" {
			return fmt.Errorf("team keys: %s: an entry has an empty team or token", k.path)
		}
		if other, dup := byToken[token]; dup {
			return fmt.Errorf("team keys: %s: teams %s and %s share a token", k.path, other, team)
		}
		byToken[token] = team
		teams = append(teams, team)
	}
	if len(byToken) == 0 {
		// A rollout that wrote {} or null must not empty the table in
		// silence: every team would get 401 with nothing in the log.
		return fmt.Errorf("team keys: %s: no team keys", k.path)
	}
	sort.Strings(teams)
	k.byToken, k.teams, k.mod = byToken, teams, st.ModTime()
	return nil
}

// ErrUnknown is a bearer no team owns.
var ErrUnknown = errors.New("unknown token")
