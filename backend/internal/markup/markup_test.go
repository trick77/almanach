package markup

import (
	"strings"
	"testing"
)

func TestFromBlocks(t *testing.T) {
	got := Normalize(FromBlocks([]Block{
		{Kind: BlockParagraph, Text: "`loom` ist ein **Chat** für viele."},
		{Kind: BlockHeading, Text: "## Woraus es besteht"},
		{Kind: BlockList, Text: "Drei Teile:", Items: []string{"`backend/`: die API", "- `ui/`: die SPA", " ", "`docs/`"}},
		{Kind: BlockNumbered, Items: []string{"1. bauen", "testen"}},
		{Kind: BlockParagraph, Text: "Stufen:"},
		{Kind: BlockList, Items: []string{"2. Stufe: Signieren", "---"}},
		{Kind: BlockHeading, Text: "  "},
		{Kind: BlockParagraph, Text: "Zeile eins<br>Zeile zwei"},
	}))
	want := "`loom` ist ein **Chat** für viele.\n\n#### Woraus es besteht\n\nDrei Teile:\n\n- `backend/`: die API\n- `ui/`: die SPA\n- `docs/`\n1. bauen\n2. testen\n\nStufen:\n\n- 2. Stufe: Signieren\n- `---`\n\nZeile eins\nZeile zwei"
	if got != want {
		t.Errorf("FromBlocks = %q, want %q", got, want)
	}
	if FromBlocks(nil) != "" {
		t.Error("no blocks must be no text")
	}
}

func TestLayoutLost(t *testing.T) {
	long := strings.Repeat("Ein Satz über das System. ", 60)
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"Ein Absatz.\n\n#### Teil\n\nNoch einer.", false},
		{"Zweck und Abgrenzung `loom` ist ein Chat. #### Struktur Das Repository besteht aus Teilen.", true},
		{"Zweck und Grenze<br>webfetch ist eine Bibliothek.", true},
		{"Ein kurzer Absatz auf einer Zeile.", false},
		// One paragraph block is stored on one line, however long.
		{long, false},
		// Marks in code are code: a statement documenting them is laid out.
		{"Ein `<br>` im Alias ist ein Zeilenumbruch.", false},
		{"Sucht mit `grep ' #### '` nach Titeln.", false},
		{"So:\n```\na #### b<br>\n```", false},
	} {
		if got := LayoutLost(c.in); got != c.want {
			t.Errorf("LayoutLost(%.40q) = %v, want %v", c.in, got, c.want)
		}
	}
}

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
		{"a title's own # stays", "## Warum C#\n### F# Interop ##\nText.", "#### Warum C#\n\n#### F# Interop\n\nText."},
		{"a fence never closed is no block", "Vorher.\n```\nText danach.\n- eins", "Vorher.\nText danach.\n\n- eins"},
		{"a fence never closed keeps its line's text", "~~~ Achtung: nie zweimal\nDanach.", "Achtung: nie zweimal\nDanach."},
		{"a fence never closed drops its word", "```go\nfoo()", "foo()"},
		{"fences never closed, one after another", "~~~ ~~~ x y\nDanach.", "x y\nDanach."},
		{"a text only headings keeps their titles", "## Zweck\n## Rollen", "Zweck\nRollen"},
		{"a heading only of a fence is gone", "## ```", ""},
		{"a text only a fence's word keeps it", "```yaml", "yaml"},
		{"two bold runs in a title stay two", "## **Lesen** und **Schreiben**\nText.", "#### **Lesen** und **Schreiben**\n\nText."},
		{"a rule is no item, a block's end", "Eins.\n* * *\nZwei.\n- - -", "Eins.\n\nZwei."},
		{"a blank line between items keeps one list", "- a\n\n- b\n\nDanach.", "- a\n- b\n\nDanach."},
		{"an item that would read as a rule is text", "* --", "* --"},
		{"a fence never closed keeps a word that is no language", "```Hinweis:\nText", "Hinweis:\nText"},
		{"a br is a newline", "#### Zweck und Grenze<br>`webfetch` ist eine Bibliothek.<BR/>#### Aufbau<br />- `fetch.go`<br>- `cache.go`",
			"#### Zweck und Grenze\n\n`webfetch` ist eine Bibliothek.\n\n#### Aufbau\n\n- `fetch.go`\n- `cache.go`"},
		{"a br in code stays", "Schreibt `<br>` nie.\n```\na<br>b\n```", "Schreibt `<br>` nie.\n\n`a<br>b`"},
		{"a whole text on one line behind #### is text", "#### Zweck und Abgrenzung `loom` ist ein selbst gehostetes Multi-User-LLM-Chat: ein Go-Backend mit JSON/SSE-API und eine eingebettete React-SPA. #### Struktur Das Repository besteht aus wenigen Teilen.\n\nDanach.",
			"Zweck und Abgrenzung `loom` ist ein selbst gehostetes Multi-User-LLM-Chat: ein Go-Backend mit JSON/SSE-API und eine eingebettete React-SPA. #### Struktur Das Repository besteht aus wenigen Teilen.\n\nDanach."},
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

// Normalize of its own output is that output: a second pass that changed
// a stored text would lose an item or write a revision per fold.
func FuzzNormalizeIdempotent(f *testing.F) {
	for _, s := range []string{"* --", "+ - -", "## ```", "## ~~~", "- a\n\n- b", "```\nx ``` y\n```", "~~~ ~~~ x", "## **a** b **c**", "- a <br\n/>b", "- a `b\nc `<br>` d`",
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
