package lang

import "testing"

// Umlauts written out or not, a text says the same; any other change is one.
func TestSameButSpelling(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Gebuehren fuer Oeffnung", "Gebühren für Öffnung", true},
		{"Aenderung", "Änderung", true},
		{"neue Queue", "neue Queue", true},
		{"für", "fur", false},
		{"Gebühr", "Gebühren", false},
	} {
		if got := SameButSpelling(c.a, c.b); got != c.want {
			t.Errorf("SameButSpelling(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// Prose trims, redacts and spells Swiss, in one call.
func TestProse(t *testing.T) {
	in := "  Der Schlüssel ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8 heißt TOKEN.  " // ship:allow-secret: invented fixture
	if got, want := Prose(in), "Der Schlüssel <redacted> heisst TOKEN."; got != want {
		t.Errorf("Prose = %q, want %q", got, want)
	}
}
