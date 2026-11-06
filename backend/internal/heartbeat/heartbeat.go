// Package heartbeat is a loop's last sign of life, stamped by the loop's
// goroutine and read by /metrics from another: whole seconds, atomic.
package heartbeat

import (
	"sync/atomic"
	"time"
)

// Beat is a loop's last stamp; its zero value has none.
type Beat struct{ unix atomic.Int64 }

// Stamp records t as the last sign of life.
func (b *Beat) Stamp(t time.Time) { b.unix.Store(t.Unix()) }

// Clear forgets the stamp: Last is zero again.
func (b *Beat) Clear() { b.unix.Store(0) }

// Last is the last stamp, UTC; zero before the first.
func (b *Beat) Last() time.Time {
	if s := b.unix.Load(); s != 0 {
		return time.Unix(s, 0).UTC()
	}
	return time.Time{}
}
