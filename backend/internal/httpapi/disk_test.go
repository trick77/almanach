package httpapi

import (
	"context"
	"math"
	"strings"
	"testing"
)

// The disk check: a folder with room passes, one without (asked for more
// than any disk has) fails naming the folder, a folder that is not there
// fails too.
func TestDiskFree(t *testing.T) {
	dir := t.TempDir()
	if err := DiskFree(dir, 1)(context.Background()); err != nil {
		t.Fatalf("a byte free: %v", err)
	}
	if err := DiskFree(dir, math.MaxUint64)(context.Background()); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("more than any disk: err = %v, want the folder named", err)
	}
	if err := DiskFree(dir+"/nope", 1)(context.Background()); err == nil {
		t.Fatal("a folder that is not there passed")
	}
}
