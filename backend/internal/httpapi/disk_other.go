//go:build !linux && !darwin

package httpapi

import "math"

// freeBytes has no statfs to ask here (the BSDs name and type its fields
// otherwise, Windows has none): every disk has room, the check passes. The
// image is Linux; this keeps the package building elsewhere.
func freeBytes(string) (uint64, error) { return math.MaxUint64, nil }
