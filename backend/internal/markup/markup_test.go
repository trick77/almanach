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
		{"a title's own # stays", "## Warum C#\n### F# Interop ##", true, "#### Warum C#\n\n#### F# Interop"},
		{"a fence never closed is no block", "Vorher.\n```\nText danach.\n- eins", true, "Vorher.\nText danach.\n\n- eins"},
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

// Plain reads the stored form alone and pairs ** as format.ts runs does:
// the cases mirror format.test.ts "plain".
func TestPlain(t *testing.T) {
	for in, want := range map[string]string{
		"#### Ablauf\n\n- **eins**\n12. zwei `a**b`\nText.": "Ablauf\n\neins\nzwei `a**b`\nText.",
		"## Zwei\n* Stern\n1) Klammer":                      "## Zwei\n* Stern\n1) Klammer",
		"a ** b\nleer **** hier\n**a\nb**":                  "a ** b\nleer **** hier\n**a\nb**",
		"**`strict`** gilt, **x":                            "`strict` gilt, **x",
		"-  zwei Blanks\n1.  auch":                          "-  zwei Blanks\n1.  auch",
	} {
		if got := Plain(in); got != want {
			t.Errorf("Plain(%q) = %q, want %q", in, got, want)
		}
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
	for _, c := range [][2]string{{"Timeout > 30 s", "Timeout < 30 s"}, {"Wert -5", "Wert 5"}, {"ab 50%", "ab 50"}} {
		if Words(c[0]) == Words(c[1]) {
			t.Errorf("%q and %q differ in a symbol, not in layout", c[0], c[1])
		}
	}
}
