package consolidate

import "time"

// heartbeat stamps the loop as alive: at every drain and before every item
// in it. A pass is at most the poll apart, an item at most a call's cap
// (inference.CallTimeout) and its writes; a hold sleeps until it ends. The
// stamp is what /metrics reads, never a readiness check: a worker holding
// on the gateway is alive and the wiki still reads.
func (w *Worker) heartbeat() { w.beat.Store(w.now().Unix()) }

// Heartbeat is when the loop last stamped itself; zero before its first
// drain.
func (w *Worker) Heartbeat() time.Time {
	if s := w.beat.Load(); s != 0 {
		return time.Unix(s, 0).UTC()
	}
	return time.Time{}
}
