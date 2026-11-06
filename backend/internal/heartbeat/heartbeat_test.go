package heartbeat

import (
	"testing"
	"time"
)

func TestBeat(t *testing.T) {
	var b Beat
	if !b.Last().IsZero() {
		t.Fatalf("zero Beat = %v", b.Last())
	}
	at := time.Date(2026, 11, 6, 12, 0, 0, 500, time.FixedZone("x", 3600))
	b.Stamp(at)
	if got := b.Last(); !got.Equal(at.Truncate(time.Second)) || got.Location() != time.UTC {
		t.Errorf("Last = %v, want %v in UTC", got, at.Truncate(time.Second))
	}
	b.Clear()
	if !b.Last().IsZero() {
		t.Errorf("Last after Clear = %v", b.Last())
	}
}
