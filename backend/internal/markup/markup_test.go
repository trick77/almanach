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
		{"a summary holds no heading", "### Zweck\nDas System.\n## Der **neue** Teil", false, "Das System."},
		{"bold and code stay inline", "Die **eine** Regel: `strict`.", true, "Die **eine** Regel: `strict`."},
		{"fence lines become code spans", "So:\n```sh\n# install\nmake `x`\n```\nFertig.", true,
			"So:\n\n`# install`\n`` make `x` ``\n\nFertig."},
		{"no heading without a space", "#Tag und -5 Grad\n1.5 Sekunden", true, "#Tag und -5 Grad\n1.5 Sekunden"},
		{"quote and table stay text", "> zitiert\n| a | b |", true, "> zitiert\n| a | b |"},
		{"a bare marker is text", "-\n#", true, "-\n#"},
		{"a title's own # stays", "## Warum C#\n### F# Interop ##", true, "#### Warum C#\n\n#### F# Interop"},
		{"a fence never closed is no block", "Vorher.\n```\nText danach.\n- eins", true, "Vorher.\nText danach.\n\n- eins"},
		{"a fence never closed keeps its line's text", "~~~ Achtung: nie zweimal\nDanach.", true, "Achtung: nie zweimal\nDanach."},
		{"a fence never closed drops its word", "```go\nfoo()", true, "foo()"},
		{"fences never closed, one after another", "~~~ ~~~ x y\nDanach.", true, "x y\nDanach."},
		{"a summary only a heading keeps its title", "## Überblick", false, "Überblick"},
		{"a text only a fence's word keeps it", "```yaml", true, "yaml"},
		{"two bold runs in a title stay two", "## **Lesen** und **Schreiben**", true, "#### **Lesen** und **Schreiben**"},
		{"a rule is no item, a block's end", "Eins.\n* * *\nZwei.\n- - -", true, "Eins.\n\nZwei."},
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

// A fence closes only with at least its own run: a ```` block quoting a
// ``` one stays code to its end, its # and - lines no heading, no list.
func TestNormalizeLongFence(t *testing.T) {
	got := Normalize("````md\n```\n# Titel\n- x\n```\n````\nDanach.", true)
	if want := "```` ``` ````\n`# Titel`\n`- x`\n```` ``` ````\n\nDanach."; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// FenceOf is CommonMark's: a backtick run with a backtick after it is a
// code span, not a fence; the whole run is the fence.
func TestFenceOf(t *testing.T) {
	for line, want := range map[string]string{"```go": "```", "````": "````", "~~~~ x": "~~~~", "```` a ``` b ````": "", "`` x ``": "", "x ```": ""} {
		if got := FenceOf(line); got != want {
			t.Errorf("FenceOf(%q) = %q, want %q", line, got, want)
		}
	}
}

// Plain reads the stored form alone and pairs ** as format.ts runs does
// (format.test.ts "plain" has these cases, read as Doc renders them).
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
	if Words("#### Fehlerfall\n\nDer Job bricht ab.") == Words("#### Erfolgsfall\n\nDer Job bricht ab.") {
		t.Error("a changed sub-heading title is a change")
	}
	if Words("`a != b`") == Words("`a == b`") {
		t.Error("a code span's operator is part of what the text says")
	}
	if Words("nicht erlaubt") == Words("erlaubt") {
		t.Error("a word dropped is a change")
	}
	for _, c := range [][2]string{{"Timeout > 30 s", "Timeout < 30 s"}, {"Wert -5", "Wert 5"}, {"ab 50%", "ab 50"},
		{"Timeout 1,5 Sekunden", "Timeout 1 5 Sekunden"}, {"liest config.yaml", "liest config yaml"},
		{"3. Deploy\n4. Rollback", "1. Deploy\n2. Rollback"}} {
		if Words(c[0]) == Words(c[1]) {
			t.Errorf("%q and %q differ in a symbol, not in layout", c[0], c[1])
		}
	}
}
