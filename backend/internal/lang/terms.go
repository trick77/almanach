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
// form stays German where it is not ("während der Auslieferung").
type term struct {
	en     string
	head   bool
	plural string
	noun   bool
	neuter bool
}

// terms are the software words a German developer says in English (user,
// 2026-11-08: "No one ever says Repositorium"). Only words whose English
// takes the German article and whose German means the software thing on a
// page: "eine Anfrage" would be "eine Request", and "Paket", "Ereignis",
// "Laufzeit" are business terms too. No prompt lists them: the code alone
// holds the rule, the prompts stay short (user, 2026-11-08). Every form
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
}

// stems is what every form above begins with: a text holding none is left
// as it is without a walk.
var stems = []string{"Dienst", "Bibliothek", "Schlüssel", "Sitzung", "Abhängigkeit", "Kennung", "Zugangsdaten", "Endpunkt",
	"Geheimnis", "Nachricht", "Umgebungsvariable", "Einstiegspunkt", "Repositori", "Warteschlange", "Zwischenspeicher", "Binär",
	"Auslieferung"}

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
			return outsideCode(s, termsIn)
		}
	}
	return s
}

// termsIn is Terms on one piece of prose with no code in it.
func termsIn(s string) string {
	var out []string // the text as it is written: words, and the runs between
	var ws []word    // the words so far, as read
	for i := 0; i < len(s); {
		if r, _ := utf8.DecodeRuneInString(s[i:]); !unicode.IsLetter(r) {
			j := i
			for j < len(s) {
				r, n := utf8.DecodeRuneInString(s[j:])
				if unicode.IsLetter(r) {
					break
				}
				j += n
			}
			out = append(out, s[i:j])
			i = j
			continue
		}
		j := i
		for j < len(s) {
			r, n := utf8.DecodeRuneInString(s[j:])
			if !unicode.IsLetter(r) {
				break
			}
			j += n
		}
		w := s[i:j]
		var prev, prev2 string
		if k := len(ws); k > 0 {
			prev = ws[k-1].low
			if k > 1 {
				prev2 = ws[k-2].low
			}
		}
		en := termFor(w, s[:i], s[j:], prev, prev2)
		if en != w && terms[w].neuter && !regender(s, i, j, out, ws) {
			en = w
		}
		ws = append(ws, word{low: strings.ToLower(w), at: len(out), start: i, end: j})
		out = append(out, en)
		i = j
	}
	return strings.Join(out, "")
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

var dativePreps = map[string]bool{
	"bei": true, "mit": true, "nach": true, "von": true, "zu": true, "aus": true, "seit": true, "in": true, "an": true,
	"auf": true, "unter": true, "über": true, "vor": true, "hinter": true, "neben": true, "zwischen": true,
}

// contracted is a preposition the neuter's "dem" merges with, as German
// writes it: "bei der Auslieferung" → "beim Deployment".
var contracted = map[string]string{"bei": "beim", "von": "vom", "zu": "zum", "in": "im", "an": "am"}

// bare are the words a noun follows with no article of its own, where a
// feminine and a neuter read the same: prepositions that take a bare noun.
var bare = map[string]bool{
	"für": true, "durch": true, "ohne": true, "gegen": true, "um": true, "als": true, "wie": true, "bis": true,
	"bei": true, "mit": true, "nach": true, "von": true, "vor": true, "per": true, "über": true,
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
// adjective or a name), a relative pronoun (a comma before "die") all
// leave it German.
func regender(s string, i, j int, out []string, ws []word) bool {
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
	case bare[prev]:
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
				return bare[ws[n-1].low]
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
	// A comma before "die": a relative pronoun, its noun before it.
	if relative[low] && art > 0 && strings.Contains(gap(art-1, ws[art].start), ",") {
		return false
	}
	if n, ok := neuterOf[low]; ok {
		out[ws[art].at] = sameCase(word0(s, ws[art]), n)
		if adj >= 0 && einType[n] && strings.HasSuffix(ws[adj].low, "e") {
			out[ws[adj].at] += "s"
		}
		return true
	}
	d, dative := dativeOf[low]
	if !dative || art == 0 || !spaced(art-1, ws[art].start) || !dativePreps[ws[art-1].low] {
		return false
	}
	prep := ws[art-1]
	if c, ok := contracted[prep.low]; ok && low == "der" {
		// "bei der" → "beim": the preposition takes the article in, the
		// space and the article go.
		out[prep.at] = sameCase(word0(s, prep), c)
		for x := prep.at + 1; x <= ws[art].at; x++ {
			out[x] = ""
		}
		return true
	}
	out[ws[art].at] = sameCase(word0(s, ws[art]), d)
	return true
}

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
