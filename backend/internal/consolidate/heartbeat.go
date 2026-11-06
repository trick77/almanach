package consolidate

import "time"

// heartbeat stamps the loop as alive: at every drain and after every item
// in it. A pass is at most the poll apart, an item at most a call's cap
// (inference.CallTimeout) and its writes; a hold sleeps until it ends. The
// stamp is what /metrics reads, never a readiness check: a worker holding
// on the gateway is alive and the wiki still reads.
func (w *Worker) heartbeat() { w.beat.Stamp(w.now()) }

// Heartbeat is when the loop last stamped itself; zero before its first
// drain.
func (w *Worker) Heartbeat() time.Time { return w.beat.Last() }
