package markup

import "testing"

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		name     string
		in       string
		headings bool
		want     string
	}{
		{"paragraphs as written", "Erster Satz.\nZweite Zeile.\n\n\n  Zweiter Absatz.  ", true,
			"Erster Satz.\nZweite Zeile.\n\nZweiter Absatz."},
		{"bullets of any marker", "Drei Fälle:\n* eins\n+ zwei\n  - drei", true,
			"Drei Fälle:\n\n- eins\n- zwei\n- drei"},
		{"numbers with a dot", "1) erst\n2. dann", true, "1. erst\n2. dann"},
		{"continuation joins its item", "- eins\n  weiter\n- zwei\n\nDanach.", true,
			"- eins weiter\n- zwei\n\nDanach."},
		{"heading any level in text", "Vorher.\n## Ablauf ##\nNachher.\n# **Fehler**", true,
			"Vorher.\n\n#### Ablauf\n\nNachher.\n\n#### Fehler"},
		{"heading in a summary is bold", "### Zweck\nDas System.", false, "**Zweck**\n\nDas System."},
		{"bold and code stay inline", "Die **eine** Regel: `strict`.", true, "Die **eine** Regel: `strict`."},
		{"fence lines become code spans", "So:\n```sh\n# install\nmake `x`\n```\nFertig.", true,
			"So:\n\n`# install`\n`` make `x` ``\n\nFertig."},
		{"no heading without a space", "#Tag und -5 Grad\n1.5 Sekunden", true, "#Tag und -5 Grad\n1.5 Sekunden"},
		{"quote and table stay text", "> zitiert\n| a | b |", true, "> zitiert\n| a | b |"},
		{"a bare marker is text", "-\n#", true, "-\n#"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Normalize(c.in, c.headings)
			if got != c.want {
				t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
			if again := Normalize(got, c.headings); again != got {
				t.Errorf("Normalize not idempotent: %q became %q", got, again)
			}
		})
	}
}

// A code line with a fence inside stays one code span, never a fence the
// next Normalize opens.
func TestNormalizeFenceInsideCode(t *testing.T) {
	got := Normalize("```\nx ``` y\n```", true)
	if want := "```` x ``` y ````"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if again := Normalize(got, true); again != got {
		t.Fatalf("not idempotent: %q", again)
	}
}

func TestPlain(t *testing.T) {
	in := "#### Ablauf\n\n- **eins**\n12. zwei `a**b`\nText."
	if got, want := Plain(in), "Ablauf\n\neins\nzwei `a**b`\nText."; got != want {
		t.Errorf("Plain = %q, want %q", got, want)
	}
}

func TestWords(t *testing.T) {
	prose := "Drei Fälle: eins, zwei und `a != b`."
	listed := "Drei Fälle:\n\n- **eins**\n- zwei und `a != b`"
	if Words(prose) != Words(listed) {
		t.Errorf("a reformat changed the words: %q vs %q", Words(prose), Words(listed))
	}
	if Words("#### Fälle\n\n"+listed) != Words(prose) {
		t.Error("a sub-heading set over a text changed its words")
	}
	if Words("`a != b`") == Words("`a == b`") {
		t.Error("a code span's operator is part of what the text says")
	}
	if Words("nicht erlaubt") == Words("erlaubt") {
		t.Error("a word dropped is a change")
	}
}
