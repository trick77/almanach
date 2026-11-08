package lang

import (
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/trick77/almanach/internal/prompts"
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
		"Das Go-Binär liest die Grösse des Binärs.":       "Das Go-Binary liest die Grösse des Binarys.",
		"Das Binaer startet, das neue Binär auch.":        "Das Binary startet, das neue Binary auch.",
		"Die Binärs liegen im Artifactory.":               "Die Binaries liegen im Artifactory.",
		// Binär as an adjective or adverb stays: no article before it.
		"Binär kodierte Felder, binär gespeichert.":                  "Binär kodierte Felder, binär gespeichert.",
		"- Binär gespeichert, von Binär nach Dezimal.":               "- Binär gespeichert, von Binär nach Dezimal.",
		"Binär- und Textdateien, eine binäre Datei, die Binärdatei.": "Binär- und Textdateien, eine binäre Datei, die Binärdatei.",
		// One form for both numbers: a word before it tells the plural.
		"Der Schlüssel des Topics.":                   "Der Key des Topics.",
		"Die Schlüssel des Topics.":                   "Die Keys des Topics.",
		"Sie liest alle geheimen Schlüssel.":          "Sie liest alle geheimen Keys.",
		"Es gibt 3 Schlüssel.":                        "Es gibt 3 Keys.",
		"Es gibt 1 Schlüssel, 10 Zwischenspeicher.":   "Es gibt 1 Key, 10 Caches.",
		"Die Klasse, die den Schlüssel hält.":         "Die Klasse, die den Key hält.",
		"Ein Teil, die einen Zwischenspeicher nutzt.": "Ein Teil, die einen Cache nutzt.",
		"Den Schlüssel liest der Dienst.":             "Den Key liest der Service.",
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
		// Umlauts written out, as older text has them.
		"Der Schluessel und die Abhaengigkeiten.": "Der Key und die Dependencies.",
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
	// A name in any case: the words made English before the case is dropped.
	if !SameName("Nachrichten und Warteschlangen", "messages und QUEUES") || SameName("Nachrichten", "Ablage") {
		t.Error("SameName does not match a stored German name to today's English, or matches another")
	}
	if got := Spelled(" Der Dienst grüßt "); got != "Der Dienst grüsst" {
		t.Errorf("Spelled = %q, want Prose without the words", got)
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

// No prompt shows the model a word the code replaces: an example in German
// steers it to the wording Terms has to undo.
func TestTermsNotInThePrompts(t *testing.T) {
	for name, p := range map[string]string{"merge": prompts.Merge, "catalogue": prompts.Catalogue, "describe": prompts.Describe,
		"project": prompts.Project, "project departure": prompts.ProjectDeparture, "repo map": prompts.RepoMap,
		"diagrams": prompts.Diagrams, "restyle": prompts.Restyle, "project restyle": prompts.ProjectRestyle, "brief": prompts.Brief} {
		for _, w := range strings.FieldsFunc(p, func(r rune) bool { return !unicode.IsLetter(r) }) {
			if _, ok := terms[w]; ok {
				t.Errorf("%s prompt says %q; Terms makes it %q", name, w, terms[w].en)
			}
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
