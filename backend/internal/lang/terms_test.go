package lang

import (
	"os"
	"strings"
	"testing"
	"unicode"
)

func TestTerms(t *testing.T) {
	for in, want := range map[string]string{
		"Der Dienst ruft die Bibliothek auf.":             "Der Service ruft die Library auf.",
		"Des Dienstes Endpunkte lesen Nachrichten.":       "Des Services Endpoints lesen Messages.",
		"Die Kennung der Sitzung ist geheim.":             "Die ID der Session ist geheim.",
		"Zugangsdaten und Geheimnisse liegen im Vault.":   "Credentials und Secrets liegen im Vault.",
		"Jede Umgebungsvariable hat einen Einstiegspunkt": "Jede Environment-Variable hat einen Entry Point",
		"Das Repositorium und seine Repositorien.":        "Das Repository und seine Repositories.",
		"Eine Warteschlange vor dem Zwischenspeicher.":    "Eine Queue vor dem Cache.",
		// One form for both numbers: a word before it tells the plural.
		"Der Schlüssel des Topics.":          "Der Key des Topics.",
		"Die Schlüssel des Topics.":          "Die Keys des Topics.",
		"Sie liest alle geheimen Schlüssel.": "Sie liest alle geheimen Keys.",
		"Es gibt 3 Schlüssel.":               "Es gibt 3 Keys.",
		"Den Schlüssel liest der Dienst.":    "Den Key liest der Service.",
		// "depending on" is no dependency.
		"Der Tarif steht in Abhängigkeit von der Region.": "Der Tarif steht in Abhängigkeit von der Region.",
		"Eine Abhängigkeit zur Library.":                  "Eine Dependency zur Library.",
		// Hyphenated: the base form leads, a joint form stays, a second part goes.
		"Die Dienst-Registry hält das Nachrichten-Format.": "Die Service-Registry hält das Nachrichten-Format.",
		"Jede Kafka-Nachricht.":                            "Jede Kafka-Message.",
		// Compounds stay: no stemming.
		"Der Projektschlüssel und die Sitzungsdauer.": "Der Projektschlüssel und die Sitzungsdauer.",
		// Code stays.
		"Die Klasse `Dienst` in Dienst.java, /api/Dienst und Dienst_v2.": "Die Klasse `Dienst` in Dienst.java, /api/Dienst und Dienst_v2.",
		"Zuerst:\n\n```\nDienst\n```\n\nDann der Dienst.":                "Zuerst:\n\n```\nDienst\n```\n\nDann der Service.",
		"Text\n\n    Dienst im Code\n":                                   "Text\n\n    Dienst im Code\n",
		// Lower case or upper case is no noun as written here.
		"dienst DIENST": "dienst DIENST",
		// A sentence end is no file name.
		"Das ist der Dienst. Danach kommt nichts.": "Das ist der Service. Danach kommt nichts.",
	} {
		if got := Terms(in); got != want {
			t.Errorf("Terms(%q) = %q, want %q", in, got, want)
		}
		if again := Terms(Terms(in)); again != Terms(in) {
			t.Errorf("Terms not idempotent on %q: %q", in, again)
		}
	}
}

// Prose and Laid, the stored forms, take the words in English; a stored
// German one is the same statement as today's English.
func TestTermsInProse(t *testing.T) {
	if got := Prose(" Der Dienst grüsst "); got != "Der Service grüsst" {
		t.Errorf("Prose = %q", got)
	}
	if got := Laid("Die Bibliothek heisst so."); got != "Die Library heisst so." {
		t.Errorf("Laid = %q", got)
	}
	if !SameButSpelling("Der Dienst liest die Warteschlange.", "Der Service liest die Queue.") {
		t.Error("a stored German word is not the same as today's English")
	}
	if SameButSpelling("Der Dienst liest.", "Der Service schreibt.") {
		t.Error("a changed verb reads as the same")
	}
}

// Every form begins with a stem, or Terms never looks for it.
func TestTermsStems(t *testing.T) {
	for de := range terms {
		ok := false
		for _, st := range stems {
			ok = ok || strings.HasPrefix(de, st)
		}
		if !ok {
			t.Errorf("%q begins with no stem: Terms would never look for it", de)
		}
	}
}

// The UI's copy keeps to the same words.
func TestTermsInTheUI(t *testing.T) {
	raw, err := os.ReadFile("../../../ui/src/strings.ts")
	if err != nil {
		t.Skipf("no UI beside the backend: %v", err)
	}
	for _, w := range strings.FieldsFunc(string(raw), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if _, ok := terms[w]; ok {
			t.Errorf("ui/src/strings.ts says %q; Terms makes it %q", w, terms[w].en)
		}
	}
}
