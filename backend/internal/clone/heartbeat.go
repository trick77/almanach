package clone

import "time"

// heartbeat stamps the poller's loop as alive: when it wakes for a cycle
// and before each repository in one. Between cycles it sleeps the sync
// interval, so a stale stamp says something only while a cycle runs
// (CycleStatus.Running): one repository's sync has taken that long.
func (p *Poller) heartbeat() { p.beat.Store(p.Now().Unix()) }

// Heartbeat is when the loop last stamped itself; zero before its first
// cycle.
func (p *Poller) Heartbeat() time.Time {
	if s := p.beat.Load(); s != 0 {
		return time.Unix(s, 0).UTC()
	}
	return time.Time{}
}
