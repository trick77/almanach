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
