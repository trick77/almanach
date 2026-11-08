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
// a form of its own.
type term struct {
	en     string
	head   bool
	plural string
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
}

// stems is what every form above begins with: a text holding none is left
// as it is without a walk.
var stems = []string{"Dienst", "Bibliothek", "Schlüssel", "Sitzung", "Abhängigkeit", "Kennung", "Zugangsdaten", "Endpunkt",
	"Geheimnis", "Nachricht", "Umgebungsvariable", "Einstiegspunkt", "Repositori", "Warteschlange", "Zwischenspeicher"}

// pluralCues are the words before a noun that make a form the same in both
// numbers ("Schlüssel") a plural: "die Schlüssel", "alle Schlüssel".
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
	var b strings.Builder
	var prev, prev2 string // the two words before, lower case
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsLetter(r) {
			b.WriteString(s[i : i+n])
			i += n
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
		b.WriteString(termFor(w, s[:i], s[j:], prev, prev2))
		prev, prev2 = strings.ToLower(w), prev
		i = j
	}
	return b.String()
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
	if t.plural != "" && (pluralCues[prev] || pluralCues[prev2] || numberBefore(before)) {
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

// numberBefore reports whether a count stands right before the word, a
// space between: "3 Schlüssel".
func numberBefore(before string) bool {
	trimmed := strings.TrimRight(before, "  ")
	if trimmed == before {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(trimmed)
	return unicode.IsDigit(r)
}
