package clone

import "time"

// heartbeat stamps the poller's loop as alive: when it wakes for a cycle,
// before each source's listing and before each repository. Between cycles
// it sleeps the sync interval, so a stale stamp says something only while
// a cycle is in flight (CycleSince): one listing or one repository's sync
// has taken that long.
func (p *Poller) heartbeat() { p.beat.Stamp(p.Now()) }

// Heartbeat is when the loop last stamped itself; zero before its first
// cycle.
func (p *Poller) Heartbeat() time.Time { return p.beat.Last() }

// CycleSince is when the cycle in flight started, its listings included
// (CycleStatus.Running starts after them, with the repositories counted);
// zero when none runs.
func (p *Poller) CycleSince() time.Time { return p.inCycle.Last() }
