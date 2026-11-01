package store

import "testing"

// TRANSITIONAL(br-repair): Remove when no revision holds <br>.
func TestRelaid(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		// webfetch's summary as stored, 2026-11-01: its first heading's marks
		// lost, the rest broken with <br>.
		{"Zweck und Grenze<br>`webfetch` ist eine Go-Bibliothek.<br>#### Aufbau<br>- `fetch.go`: holt<br>- `cache.go`: hält",
			"#### Zweck und Grenze\n\n`webfetch` ist eine Go-Bibliothek.\n\n#### Aufbau\n\n- `fetch.go`: holt\n- `cache.go`: hält"},
		{"Ein Satz.<br>Noch einer.", "Ein Satz.\nNoch einer."},
		{"Ohne Umbruch, wie gespeichert.\n\n#### Teil\n\nText.", "Ohne Umbruch, wie gespeichert.\n\n#### Teil\n\nText."},
		{"Schreibt `<br>` nie.", "Schreibt `<br>` nie."},
	} {
		if got := relaid(c.in); got != c.want {
			t.Errorf("relaid(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
