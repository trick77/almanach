package clone

import "time"

// heartbeat stamps the poller's loop as alive: when it wakes for a cycle,
// before each source's listing and before each repository. Between cycles
// it sleeps the sync interval, so a stale stamp says something only once
// the next cycle is due: one listing or one repository's sync has taken
// that long.
func (p *Poller) heartbeat() { p.beat.Store(p.Now().Unix()) }

// Heartbeat is when the loop last stamped itself; zero before its first
// cycle.
func (p *Poller) Heartbeat() time.Time {
	if s := p.beat.Load(); s != 0 {
		return time.Unix(s, 0).UTC()
	}
	return time.Time{}
}
