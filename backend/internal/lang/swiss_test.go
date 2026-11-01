package lang

import (
	"strings"
	"testing"
)

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

// Laid redacts before it lays out: a key in a fence would be code spans,
// one in a list item one joined line, and the PEM shape gone either way.
func TestLaidRedactsFirst(t *testing.T) {
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEinventedAAAAAAAAAAAA\n-----END RSA PRIVATE KEY-----" // ship:allow-secret: invented fixture
	for _, in := range []string{"Schlüssel:\n```\n" + pem + "\n```", "- Schlüssel\n" + pem} {
		got := Laid(in, true)
		if strings.Contains(got, "MIIE") || !strings.Contains(got, "<redacted>") {
			t.Errorf("Laid(%q) = %q, want the key redacted", in, got)
		}
	}
	if got, want := Laid("## Grösse\n* **Maße**", true), "#### Grösse\n\n- **Masse**"; got != want {
		t.Errorf("Laid = %q, want %q", got, want)
	}
}
