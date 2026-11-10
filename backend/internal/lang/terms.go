package lang

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// term is one German form of a software word and the English one it
// becomes. head: the form may open a hyphenated compound ("Dienst-Registry"
// → "Service-Registry"); an inflected one there is a joint ("Nachrichten-
// Format"), left alone. plural: the English for the same German form read
// as a plural ("die Schlüssel" → "die Keys"), "" when the German plural is
// a form of its own. noun: the form is an adjective or adverb as often
// ("Binär kodiert", "Binär- und Textdateien"), so it is the software word
// only after an article or determiner, one word between at most ("das
// Binär", "des neuen Binärs"), or as a hyphenated compound's last part
// ("Go-Binär"). neuter: the German form is feminine and the English a
// neuter ("die Auslieferung" → "das Deployment"): the article before it,
// an adjective between, are made neuter where the case is sure, and the
// form stays German where it is not ("während der Auslieferung"). tail:
// the form is an everyday word ("zwei Köpfe", "der Fluss", "im Lauf der
// Zeit"), the software word only as a hyphenated compound's last part
// after an acronym ("CORS-Köpfe", "OIDC-Fluss", "CI-Lauf"): after a word
// it is as often the everyday one ("Brief-Kopf", "Geld-Fluss",
// "Zug-Lauf").
type term struct {
	en     string
	head   bool
	plural string
	noun   bool
	neuter bool
	tail   bool
}

// terms are the software words a German developer says in English (user,
// 2026-11-08: "No one ever says Repositorium"). Only words whose English
// takes the German article and whose German means the software thing on a
// page: "eine Anfrage" would be "eine Request", and "Paket", "Ereignis",
// "Laufzeit" are business terms too. No prompt lists them, the prompts stay
// short (user, 2026-11-08): they state the rule in one sentence, no word
// named (prompts.Jargon), for what code cannot be sure of. Every form
// spelled out: no stemming, no compound but by hyphen.
var terms = map[string]term{
	"Dienst": {en: "Service", head: true}, "Dienste": {en: "Services"}, "Dienstes": {en: "Services"}, "Diensten": {en: "Services"},
	"Bibliothek": {en: "Library", head: true}, "Bibliotheken": {en: "Libraries"},
	"Schlüssel": {en: "Key", head: true, plural: "Keys"}, "Schlüssels": {en: "Keys"}, "Schlüsseln": {en: "Keys"},
	"Sitzung": {en: "Session", head: true}, "Sitzungen": {en: "Sessions"},
	"Abhängigkeit": {en: "Dependency", head: true}, "Abhängigkeiten": {en: "Dependencies"},
	"Kennung": {en: "ID", head: true}, "Kennungen": {en: "IDs"},
	"Zugangsdaten": {en: "Credentials", head: true},
	"Endpunkt":     {en: "Endpoint", head: true}, "Endpunkte": {en: "Endpoints"}, "Endpunkten": {en: "Endpoints"},
	"Endpunkts": {en: "Endpoints"}, "Endpunktes": {en: "Endpoints"},
	"Geheimnis": {en: "Secret", head: true}, "Geheimnisse": {en: "Secrets"}, "Geheimnisses": {en: "Secrets"}, "Geheimnissen": {en: "Secrets"},
	"Nachricht": {en: "Message", head: true}, "Nachrichten": {en: "Messages"},
	"Umgebungsvariable": {en: "Environment-Variable"}, "Umgebungsvariablen": {en: "Environment-Variablen"},
	"Einstiegspunkt": {en: "Entry Point"}, "Einstiegspunkte": {en: "Entry Points"}, "Einstiegspunkten": {en: "Entry Points"},
	"Einstiegspunkts": {en: "Entry Points"}, "Einstiegspunktes": {en: "Entry Points"},
	"Repositorium": {en: "Repository", head: true}, "Repositoriums": {en: "Repositorys"}, "Repositorien": {en: "Repositories"},
	"Warteschlange": {en: "Queue", head: true}, "Warteschlangen": {en: "Queues"},
	"Zwischenspeicher": {en: "Cache", head: true, plural: "Caches"}, "Zwischenspeichers": {en: "Caches"}, "Zwischenspeichern": {en: "Caches"},
	"Binär": {en: "Binary", noun: true}, "Binärs": {en: "Binarys", plural: "Binaries", noun: true},
	// A delivery of goods too, in a logistics domain; taken as the
	// software's like "Nachricht" (user, 2026-11-09: "wie die übrigen
	// Fachbegriffe, die wir übersetzen").
	"Auslieferung": {en: "Deployment", neuter: true}, "Auslieferungen": {en: "Deployments"},
	"Auslieferungs": {en: "Deployment", head: true},
	"Sitzungs":      {en: "Session", head: true},
	// The model's word-by-word German for Go jargon, pages a reader could not
	// follow (user, 2026-11-10): "HTTP-Rohrleitungen", "CORS-Köpfe",
	// "OIDC-Fluss", "rettet Paniken". Everyday words alone, so only after an
	// acronym (tail); "Paniken" no one says of fear, "Panik" one does ("in
	// Panik"), so it stays. Left to the prompt (prompts.Jargon): "Strom" ("PV-
	// Strom"), "Drosselung" (a pump's, a line's), "Boden" ("CH-Boden", data
	// residency), and a calque with no hyphen.
	"Rohrleitung": {en: "Pipeline", tail: true}, "Rohrleitungen": {en: "Pipelines", tail: true},
	"Kopf": {en: "Header", tail: true}, "Kopfs": {en: "Headers", tail: true}, "Kopfes": {en: "Headers", tail: true},
	"Köpfe": {en: "Header", tail: true}, "Köpfen": {en: "Headern", tail: true},
	"Fluss": {en: "Flow", tail: true}, "Flusses": {en: "Flows", tail: true}, "Flüsse": {en: "Flows", tail: true},
	"Flüssen": {en: "Flows", tail: true},
	"Lauf":    {en: "Run", tail: true}, "Laufs": {en: "Runs", tail: true}, "Laufes": {en: "Runs", tail: true},
	"Läufe": {en: "Runs", tail: true}, "Läufen": {en: "Runs", tail: true},
	"Schreiber": {en: "Writer", tail: true}, "Schreibers": {en: "Writers", tail: true}, "Schreibern": {en: "Writern", tail: true},
	"Paniken": {en: "Panics"},
}

// Older text may write the umlauts out ("Schluessel", "Abhaengigkeit"),
// which SameButSpelling tolerates: those forms are the same words.
func init() {
	for de, t := range terms {
		if out := umlauts.Replace(de); out != de {
			terms[out] = t
		}
	}
	for _, st := range stems {
		if out := umlauts.Replace(st); out != st {
			stems = append(stems, out)
		}
	}
	// A tail form's stem is the form with its hyphen before it: "Ablauf",
	// "Kopfzeile" are everyday words, "-Kopf" is not.
	for de, t := range terms {
		if t.tail {
			stems = append(stems, "-"+de)
		}
	}
}

// stems is what every form above but a tail form begins with: a text
// holding none is left as it is without a walk.
var stems = []string{"Dienst", "Bibliothek", "Schlüssel", "Sitzung", "Abhängigkeit", "Kennung", "Zugangsdaten", "Endpunkt",
	"Geheimnis", "Nachricht", "Umgebungsvariable", "Einstiegspunkt", "Repositori", "Warteschlange", "Zwischenspeicher", "Binär",
	"Auslieferung", "Paniken"}

// singularCues are the articles that make such a form a singular, whatever
// stands before them: "die Klasse, die den Schlüssel hält" holds one key.
var singularCues = map[string]bool{
	"der": true, "den": true, "dem": true, "des": true, "ein": true, "einen": true, "einem": true, "eines": true,
	"kein": true, "keinen": true, "keinem": true, "keines": true, "jeder": true, "jeden": true, "jedem": true, "jedes": true,
	"diesen": true, "diesem": true, "dieses": true, "sein": true, "seinen": true, "seinem": true, "ihren": true, "ihrem": true,
}

// pluralCues are the words before a noun that make a form the same in both
// numbers ("Schlüssel") a plural: "die Schlüssel", "alle Schlüssel", and
// two words back across an adjective, "alle geheimen Schlüssel".
var pluralCues = map[string]bool{
	"die": true, "alle": true, "keine": true, "mehrere": true, "beide": true, "beiden": true, "diese": true, "jene": true,
	"welche": true, "solche": true, "viele": true, "vielen": true, "einige": true, "wenige": true, "ihre": true, "seine": true,
	"unsere": true, "deren": true, "zwei": true, "drei": true, "vier": true, "fünf": true,
}

// Terms writes the model's German prose with the software words in
// English, as a German developer says them: "der Dienst" becomes "der
// Service". Like SwissSS, a blunt rule on every stored write, and text
// stored before it reads the same once respelled (store.Respell). Code
// stays: fenced and indented blocks, backtick spans, and a
// word that is part of a path, a file name or an identifier ("Dienst.java",
// "/dienst", "Dienst_v2"). "in Abhängigkeit von" means "depending on" and
// stays. Deterministic and idempotent: no English form holds a German one.
func Terms(s string) string {
	for _, st := range stems {
		if strings.Contains(s, st) {
			return proseIn(s, termsIn)
		}
	}
	return s
}

// termsIn is Terms on one piece of prose with no code in it, before and
// after it the text around it.
func termsIn(before, s, after string) string {
	var out []string // the text as it is written: words, and the runs between
	var ws []word    // the words so far, as read
	for i := 0; i < len(s); {
		if r, _ := utf8.DecodeRuneInString(s[i:]); !unicode.IsLetter(r) {
			j := runEnd(s, i, false)
			out = append(out, s[i:j])
			i = j
			continue
		}
		j := runEnd(s, i, true)
		w := s[i:j]
		var prev, prev2 string
		if k := len(ws); k > 0 {
			prev = ws[k-1].low
			if k > 1 {
				prev2 = ws[k-2].low
			}
		}
		en := termFor(w, s[:i], s[j:], prev, prev2)
		if en != w && terms[w].neuter && !regender(before, s, after, i, j, out, ws) {
			en = w
		}
		ws = append(ws, word{low: strings.ToLower(w), at: len(out), start: i, end: j})
		out = append(out, en)
		i = j
	}
	return strings.Join(out, "")
}

// runEnd is where the run of letters (or of anything but letters) that
// starts at s[i] ends.
func runEnd(s string, i int, letters bool) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if unicode.IsLetter(r) != letters {
			break
		}
		i += n
	}
	return i
}

// word is one word of a text: lower case, its index in the text as
// written, and where it stands in the text as read.
type word struct {
	low        string
	at         int
	start, end int
}

// neuterOf is a feminine article in the nominative or accusative, and
// "zur", as the neuter says it; einType those of them an adjective after
// takes "-es" behind ("eine neue" → "ein neues").
var neuterOf = map[string]string{
	"die": "das", "diese": "dieses", "jene": "jenes", "welche": "welches", "jede": "jedes", "zur": "zum",
	"eine": "ein", "keine": "kein", "seine": "sein", "ihre": "ihr", "unsere": "unser", "meine": "mein", "deine": "dein",
	"eure": "euer",
}

var einType = map[string]bool{
	"ein": true, "kein": true, "sein": true, "ihr": true, "unser": true, "mein": true, "dein": true, "euer": true,
}

// dativeOf is a feminine article that is a dative or a genitive, as the
// neuter's dative says it: only after a preposition that takes the dative
// is the case sure.
var dativeOf = map[string]string{
	"der": "dem", "einer": "einem", "keiner": "keinem", "dieser": "diesem", "jener": "jenem", "jeder": "jedem",
	"seiner": "seinem", "ihrer": "ihrem", "unserer": "unserem", "eurer": "eurem",
}

// prep is a word before a noun, each in one place: bare, a noun follows it
// with no article of its own, where a feminine and a neuter read the same
// ("für Auslieferung"; "als", "wie" too, though no prepositions); dative,
// a feminine "der" after it is a dative, never a genitive ("bei der", "in
// der"); into, the neuter's "dem" merges with it, as German writes it
// ("bei der Auslieferung" → "beim Deployment"), a dative's alone.
type prep struct {
	bare, dative bool
	into         string
}

var preps = map[string]prep{
	"für": {bare: true}, "durch": {bare: true}, "ohne": {bare: true}, "gegen": {bare: true}, "um": {bare: true},
	"als": {bare: true}, "wie": {bare: true}, "bis": {bare: true}, "per": {bare: true},
	"mit": {bare: true, dative: true}, "nach": {bare: true, dative: true}, "vor": {bare: true, dative: true},
	"über": {bare: true, dative: true},
	"bei":  {bare: true, dative: true, into: "beim"}, "von": {bare: true, dative: true, into: "vom"},
	"zu": {dative: true, into: "zum"}, "in": {dative: true, into: "im"}, "an": {dative: true, into: "am"},
	"aus": {dative: true}, "seit": {dative: true}, "auf": {dative: true}, "unter": {dative: true},
	"hinter": {dative: true}, "neben": {dative: true}, "zwischen": {dative: true},
}

// relative are the articles that open a relative clause as its pronoun.
var relative = map[string]bool{"die": true, "der": true, "welche": true}

// joins are the words that join two nouns, which may share one article
// before the first ("die Entwicklung und Auslieferung").
var joins = map[string]bool{"und": true, "oder": true, "sowie": true}

// regender makes a neuter term at s[i:j] fit the words before it, in out,
// the text as written: true when it stands, false where the code cannot be
// sure of the case or the gender of what governs it, and the term stays
// German (user, 2026-11-09: never "die Deployment"). It stands only:
//   - after a feminine article (and an adjective), made neuter: "die
//     (neue)" → "das (neue)", "eine neue" → "ein neues", "zur" → "zum";
//   - after a feminine dative article that a dative preposition governs:
//     "bei der" → "beim", "mit einer" → "mit einem";
//   - after a preposition that takes a bare noun ("per", "für");
//   - after "und", "oder" joining it to bare nouns, one or a list ("Build,
//     Test und"), no article before the first;
//   - first in a line, a list item's or heading's marker at most before it.
//
// A compound's last part ("Go-Auslieferung"), a word set off by anything
// but spaces ("die **Auslieferung**"), a capitalised word before it (an
// adjective or a name), a relative pronoun (a comma before "die", a line
// break between them too, unless "die" stands right before it and no verb
// can follow before the block ends: "Build, die Auslieferung und Tests.")
// all leave it German.
// before and after are the text around s, the piece.
func regender(before, s, after string, i, j int, out []string, ws []word) bool {
	if strings.HasSuffix(s[:i], "-") || strings.HasPrefix(s[j:], "-") {
		return false
	}
	gap := func(k, next int) string { return s[ws[k].end:next] }
	spaced := func(k, next int) bool { return gap(k, next) != "" && strings.TrimSpace(gap(k, next)) == "" }
	k := len(ws) - 1
	if k < 0 {
		// First in its piece: a line's start, a list item's or heading's
		// marker at most before it, never a code span's end (" Auslieferung"
		// after "`x`").
		return lineStart(s[:i])
	}
	if !spaced(k, i) {
		return false
	}
	prev := ws[k].low
	switch {
	case preps[prev].bare:
		return true
	case joins[prev]:
		// The nouns joined, walked back over a list's commas: each
		// capitalised and no article, the first bare (first, after a
		// clause's end, or after a preposition that takes it so).
		n := k - 1
		if n < 0 || !spaced(n, ws[k].start) {
			return false
		}
		for {
			if !startsUpper(word0(s, ws[n])) || isArticle(ws[n].low) {
				return false
			}
			if n == 0 {
				return lineStart(s[:ws[0].start])
			}
			switch g := strings.TrimSpace(gap(n-1, ws[n].start)); g {
			case "":
				return preps[ws[n-1].low].bare
			case ",":
				n--
			default:
				return strings.Trim(g, ".:;!?") == ""
			}
		}
	}
	art, adj := k, -1
	if !isArticle(prev) {
		if startsUpper(word0(s, ws[k])) || k == 0 || !spaced(k-1, ws[k].start) || !isArticle(ws[k-1].low) {
			return false
		}
		art, adj = k-1, k
	}
	// Two determiners ("die eine"): the first governs, the code cannot
	// say how; German.
	if art > 0 && spaced(art-1, ws[art].start) && isArticle(ws[art-1].low) {
		return false
	}
	low := ws[art].low
	// A comma before "die": a relative pronoun, its noun before it, a code
	// span's or the line before's too; an article only where no verb can
	// follow it.
	lead := before + s[:ws[art].start]
	if art > 0 {
		lead = gap(art-1, ws[art].start)
	}
	if x := strings.LastIndexFunc(lead, unicode.IsLetter); x >= 0 {
		_, n := utf8.DecodeRuneInString(lead[x:])
		lead = lead[x+n:]
	}
	if relative[low] && strings.Contains(lead, ",") {
		head := before + s[:i]
		item := listItem(head[strings.LastIndexByte(head, '\n')+1:])
		if adj >= 0 || !verbless(item, s[j:], after) {
			return false
		}
	}
	if n, ok := neuterOf[low]; ok {
		out[ws[art].at] = sameCase(word0(s, ws[art]), n)
		if adj >= 0 && einType[n] && strings.HasSuffix(ws[adj].low, "e") {
			out[ws[adj].at] += "s"
		}
		return true
	}
	d, dative := dativeOf[low]
	if !dative || art == 0 || !spaced(art-1, ws[art].start) || !preps[ws[art-1].low].dative {
		return false
	}
	p := ws[art-1]
	if c := preps[p.low].into; c != "" && low == "der" {
		// "bei der" → "beim": the preposition takes the article in, the
		// space and the article go.
		out[p.at] = sameCase(word0(s, p), c)
		for x := p.at + 1; x <= ws[art].at; x++ {
			out[x] = ""
		}
		return true
	}
	out[ws[art].at] = sameCase(word0(s, ws[art]), d)
	return true
}

// verbless reports whether rest, a piece's text after a term, and after,
// the text after the piece, end the block (blockEnds) with no verb between:
// capitalised nouns, joins and plain articles alone, spaces and commas
// between, one ".", "!" or "?" at most. A relative clause has its verb last
// and lower case, so a "die" before the term is then an article. A sentence
// end inside a block is never taken: a "." may close an abbreviation ("St.
// Gallen", "Nr. 3"), a "!" a name or an image ("![Bild]"), and the verb
// may follow in the next line or a code span.
func verbless(item bool, rest, after string) bool {
	for i := 0; i < len(rest); {
		r, n := utf8.DecodeRuneInString(rest[i:])
		switch {
		case unicode.IsLetter(r):
			j := runEnd(rest, i, true)
			if w := rest[i:j]; !startsUpper(w) && !joins[w] && !plainArticle(w) {
				return false
			}
			i = j
		case r == ',' || unicode.IsSpace(r):
			i += n
		case r == '.' || r == '!' || r == '?':
			if strings.TrimSpace(rest[i+n:]) != "" {
				return false
			}
			i = len(rest)
		default:
			return false
		}
	}
	return blockEnds(item, rest, after)
}

// blockEnds reports whether a piece ending in rest is its block's last
// words: the text ends after it, or a line break does and a blank line, a
// heading ("## ") or, in a list, the next item follows. A code span after
// it, or a line going on, is not; nor a "- " after a line of prose (a dash
// a wrapped line may begin with). item: the piece's line is a list item.
func blockEnds(item bool, rest, after string) bool {
	if after == "" {
		return true
	}
	if !strings.HasSuffix(rest, "\n") {
		return false
	}
	next, _, _ := strings.Cut(after, "\n")
	m := strings.TrimLeft(next, " ")
	h := strings.TrimLeft(m, "#")
	return m == "" || len(h) < len(m) && strings.HasPrefix(h, " ") || item && listItem(next)
}

// listItem reports whether a line opens a list item: "- ", "* ", "+ " or
// a number and "." or ")" before a space.
func listItem(line string) bool {
	m := strings.TrimLeft(line, " ")
	if strings.HasPrefix(m, "- ") || strings.HasPrefix(m, "* ") || strings.HasPrefix(m, "+ ") {
		return true
	}
	n := strings.TrimLeft(m, "0123456789")
	return len(n) < len(m) && (strings.HasPrefix(n, ". ") || strings.HasPrefix(n, ") "))
}

// plainArticle reports whether w is an article or determiner, as the
// tables above hold them, that no verb is spelled like.
func plainArticle(w string) bool { return (isArticle(w) || nounCue(w)) && !verbForms[w] }

// verbForms are determiners spelled like a verb's finite form or its
// infinitive ("einen", "einigen": unite, agree; "meinen": mean), which
// could be a relative clause's last word.
var verbForms = map[string]bool{"einen": true, "eine": true, "einige": true, "meine": true, "sein": true}

// lineStart reports whether before, a piece's text before a word, is a
// line's start: nothing, or a list item's or heading's marker ("- ", "1. ",
// "## "), never bare spaces, which a code span leaves.
func lineStart(before string) bool {
	if before == "" {
		return true
	}
	m := strings.TrimSpace(before)
	if m == "" || !strings.HasSuffix(before, " ") {
		return false
	}
	return strings.Trim(m, "-*+#>0123456789.") == ""
}

// word0 is a word as written.
func word0(s string, w word) string { return s[w.start:w.end] }

func isArticle(w string) bool {
	_, n := neuterOf[w]
	_, d := dativeOf[w]
	return n || d
}

func startsUpper(w string) bool {
	r, _ := utf8.DecodeRuneInString(w)
	return unicode.IsUpper(r)
}

// sameCase is to written as was: capital first when was is.
func sameCase(was, to string) string {
	if !startsUpper(was) {
		return to
	}
	r, n := utf8.DecodeRuneInString(to)
	return string(unicode.ToUpper(r)) + to[n:]
}

// termFor is the word w as written, before and after the text around it,
// prev and prev2 the two words before it.
func termFor(w, before, after, prev, prev2 string) string {
	t, ok := terms[w]
	if !ok || partOfCode(before, after) {
		return w
	}
	if strings.HasPrefix(after, "-") && !t.head {
		return w
	}
	if t.tail && !acronymJoined(before) {
		return w
	}
	if w == "Abhängigkeit" && prev == "in" {
		return w
	}
	if t.noun && !strings.HasSuffix(before, "-") && !nounCue(prev) && !nounCue(prev2) {
		return w
	}
	if t.plural != "" && !singularCues[prev] && (pluralCues[prev] || pluralCues[prev2] || numberBefore(before)) {
		return t.plural
	}
	return t.en
}

// acronymJoined reports whether before, the text before a word, ends in an
// acronym and a hyphen: two characters or more, capitals, digits and
// slashes, at least one capital, a space, an opening mark or a line's
// start before them ("HTTP-", "OIDC-", "S3-", "CI/CD-", "HTTP/2-"; never
// "Brief-", "PV-Anlagen-", "X-").
func acronymJoined(before string) bool {
	head, ok := strings.CutSuffix(before, "-")
	if !ok {
		return false
	}
	i := strings.LastIndexFunc(head, func(r rune) bool { return !unicode.IsUpper(r) && !unicode.IsDigit(r) && r != '/' })
	a := head
	if i >= 0 {
		r, n := utf8.DecodeRuneInString(head[i:])
		if !unicode.IsSpace(r) && !strings.ContainsRune("([„\"'«*_`", r) {
			return false
		}
		a = head[i+n:]
	}
	return utf8.RuneCountInString(a) >= 2 && strings.IndexFunc(a, unicode.IsUpper) >= 0
}

// partOfCode reports whether a word stands in a path, a file name or an
// identifier: a path or identifier character beside it, or a dot right
// after it that a letter follows.
func partOfCode(before, after string) bool {
	if r, _ := utf8.DecodeLastRuneInString(before); r != utf8.RuneError && (unicode.IsDigit(r) || strings.ContainsRune(`/\._@#$:`, r)) {
		return true
	}
	r, n := utf8.DecodeRuneInString(after)
	if r == utf8.RuneError {
		return false
	}
	if unicode.IsDigit(r) || strings.ContainsRune(`/\_@#$(`, r) {
		return true
	}
	if r == '.' || r == ':' {
		next, _ := utf8.DecodeRuneInString(after[n:])
		return unicode.IsLetter(next)
	}
	return false
}

// nounCue reports whether a word before a form makes it a noun: an article
// or determiner of either number, the neuter's too.
func nounCue(w string) bool {
	return singularCues[w] || pluralCues[w] || w == "das" || w == "jenes" || w == "welches"
}

// numberBefore reports whether a count above one stands right before the
// word, a space between: "3 Schlüssel", never "1 Schlüssel".
func numberBefore(before string) bool {
	trimmed := strings.TrimRight(before, "  ")
	if trimmed == before {
		return false
	}
	start := len(trimmed)
	for start > 0 {
		r, n := utf8.DecodeLastRuneInString(trimmed[:start])
		if !unicode.IsDigit(r) {
			break
		}
		start -= n
	}
	digits := strings.TrimLeft(trimmed[start:], "0")
	return start < len(trimmed) && digits != "1"
}
