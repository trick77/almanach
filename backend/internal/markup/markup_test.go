package markup

import "testing"

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want string
	}{
		{"paragraphs as written", "Erster Satz.\nZweite Zeile.\n\n\n  Zweiter Absatz.  ",
			"Erster Satz.\nZweite Zeile.\n\nZweiter Absatz."},
		{"bullets of any marker", "Drei Fälle:\n* eins\n+ zwei\n  - drei",
			"Drei Fälle:\n\n- eins\n- zwei\n- drei"},
		{"numbers with a dot", "1) erst\n2. dann", "1. erst\n2. dann"},
		{"continuation joins its item", "- eins\n  weiter\n- zwei\n\nDanach.",
			"- eins weiter\n- zwei\n\nDanach."},
		{"heading any level in text", "Vorher.\n## Ablauf ##\nNachher.\n# **Fehler**",
			"Vorher.\n\n#### Ablauf\n\nNachher.\n\n#### Fehler"},
		{"a summary laid out as a statement", "### Zweck\nDas **System** für alle.\n## Rollen\n* `api`: Dienst\n* `ui`: Oberfläche",
			"#### Zweck\n\nDas **System** für alle.\n\n#### Rollen\n\n- `api`: Dienst\n- `ui`: Oberfläche"},
		{"bold and code stay inline", "Die **eine** Regel: `strict`.", "Die **eine** Regel: `strict`."},
		{"fence lines become code spans", "So:\n```sh\n# install\nmake `x`\n```\nFertig.",
			"So:\n\n`# install`\n`` make `x` ``\n\nFertig."},
		{"no heading without a space", "#Tag und -5 Grad\n1.5 Sekunden", "#Tag und -5 Grad\n1.5 Sekunden"},
		{"quote and table stay text", "> zitiert\n| a | b |", "> zitiert\n| a | b |"},
		{"a bare marker is text", "-\n#", "-\n#"},
		{"a title's own # stays", "## Warum C#\n### F# Interop ##", "#### Warum C#\n\n#### F# Interop"},
		{"a fence never closed is no block", "Vorher.\n```\nText danach.\n- eins", "Vorher.\nText danach.\n\n- eins"},
		{"a fence never closed keeps its line's text", "~~~ Achtung: nie zweimal\nDanach.", "Achtung: nie zweimal\nDanach."},
		{"a fence never closed drops its word", "```go\nfoo()", "foo()"},
		{"fences never closed, one after another", "~~~ ~~~ x y\nDanach.", "x y\nDanach."},
		{"a text only a heading is that heading", "## Überblick", "#### Überblick"},
		{"a text only a fence's word keeps it", "```yaml", "yaml"},
		{"two bold runs in a title stay two", "## **Lesen** und **Schreiben**", "#### **Lesen** und **Schreiben**"},
		{"a rule is no item, a block's end", "Eins.\n* * *\nZwei.\n- - -", "Eins.\n\nZwei."},
		{"a blank line between items keeps one list", "- a\n\n- b\n\nDanach.", "- a\n- b\n\nDanach."},
		{"an item that would read as a rule is text", "* --", "* --"},
		{"a fence never closed keeps a word that is no language", "```Hinweis:\nText", "Hinweis:\nText"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := Normalize(c.in)
			if got != c.want {
				t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
			}
			if again := Normalize(got); again != got {
				t.Errorf("Normalize not idempotent: %q became %q", got, again)
			}
		})
	}
}

// A code line with a fence inside stays one code span, never a fence the
// next Normalize opens.
func TestNormalizeFenceInsideCode(t *testing.T) {
	got := Normalize("```\nx ``` y\n```")
	if want := "```` x ``` y ````"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if again := Normalize(got); again != got {
		t.Fatalf("not idempotent: %q", again)
	}
}

// A fence closes only with at least its own run: a ```` block quoting a
// ``` one stays code to its end, its # and - lines no heading, no list.
func TestNormalizeLongFence(t *testing.T) {
	got := Normalize("````md\n```\n# Titel\n- x\n```\n````\nDanach.")
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

// Normalize of its own output is that output: Relaid reads every stored
// text through it, and a second pass that changed a text would lose an
// item or write a revision per fold.
func FuzzNormalizeIdempotent(f *testing.F) {
	for _, s := range []string{"* --", "+ - -", "## ```", "## ~~~", "- a\n\n- b", "```\nx ``` y\n```", "~~~ ~~~ x", "## **a** b **c**",
		"Fälle:\n* eins\n  weiter\n\n1) zwei", "```go\nfoo", "# Titel\n---\nText", "- # x\n> y", "````md\n```\n# x\n```\n````", "```Hinweis:\nText"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		once := Normalize(s)
		if twice := Normalize(once); twice != once {
			t.Errorf("Normalize(%q) = %q, again %q", s, once, twice)
		}
	})
}
