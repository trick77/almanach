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
		// Dependency goes into German, a compound's head with its joint; an
		// English term it opens stays, as does German already there.
		"Der Tarif steht in Abhängigkeit von der Region.":                        "Der Tarif steht in Abhängigkeit von der Region.",
		"Eine Dependency zur Library, die Dependencies des Repos.":               "Eine Abhängigkeit zur Library, die Abhängigkeiten des Repos.",
		"Der Dependency-Graph und die Maven-Dependency.":                         "Der Abhängigkeits-Graph und die Maven-Abhängigkeit.",
		"Per Dependency Injection, Dependency-Injection, Dependency Check.":      "Per Dependency Injection, Dependency-Injection, Dependency Check.",
		"OWASP Dependency-Check, Dependency-Track, das Maven Dependency Plugin.": "OWASP Dependency-Check, Dependency-Track, das Maven Dependency Plugin.",
		// The English terms keep only Dependency: other terms before such a
		// word go into English as before.
		"Das Schlüssel Management und die Sitzung Scope.":      "Das Key Management und die Session Scope.",
		"Das Dependency-Management.":                           "Das Abhängigkeits-Management.",
		"Dependabot hebt die `Dependency` in Dependency.java.": "Dependabot hebt die `Dependency` in Dependency.java.",
		"Eine Dependency. Graph folgt.":                        "Eine Abhängigkeit. Graph folgt.",
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
		"Der Schluessel und die Sitzungen.": "Der Key und die Sessions.",
		// A sentence end is no file name.
		"Das ist der Dienst. Danach kommt nichts.": "Das ist der Service. Danach kommt nichts.",
		// Word-by-word Go jargon: the software word as a compound's last part
		// after an acronym; after a word, or alone, the everyday word.
		"Sie bündelt die HTTP-Rohrleitungen, setzt Sicherheits- und CORS-Köpfe.": "Sie bündelt die HTTP-Pipelines, setzt Sicherheits- und CORS-Header.",
		"Ein OIDC-Fluss, der CI-Lauf, (S3-Schreiber), [HTTP-Kopf].":              "Ein OIDC-Flow, der CI-Run, (S3-Writer), [HTTP-Header].",
		"Der CI/CD-Lauf, die HTTP/2-Köpfe, auf CH-Boden.":                        "Der CI/CD-Run, die HTTP/2-Header, auf CH-Boden.",
		"„X-Kopf, «A-Lauf,  X-Fluss, kit/X-Kopf.":                                "„X-Kopf, «A-Lauf,  X-Fluss, kit/X-Kopf.",
		"Der CSV-Schreiber im Test-Lauf, den HTTP-Köpfen.":                       "Der CSV-Writer im Test-Lauf, den HTTP-Headern.",
		"Der Kopf, zwei Köpfe, der Fluss, im Lauf der Zeit, auf dem Boden.":      "Der Kopf, zwei Köpfe, der Fluss, im Lauf der Zeit, auf dem Boden.",
		"Der Brief-Kopf, der Geld-Fluss, der Zug-Lauf, der Holz-Boden.":          "Der Brief-Kopf, der Geld-Fluss, der Zug-Lauf, der Holz-Boden.",
		"Der Gerichts-Schreiber, PV-Anlagen-Kopf, ein Fluss-Lauf, Ab-Lauf.":      "Der Gerichts-Schreiber, PV-Anlagen-Kopf, ein Fluss-Lauf, Ab-Lauf.",
		"Der PV-Strom, die Drosselung, die Rohrleitung leckt, der Schreiber.":    "Der PV-Strom, die Drosselung, die Rohrleitung leckt, der Schreiber.",
		"Sie rettet Paniken, nie in Panik.":                                      "Sie rettet Panics, nie in Panik.",
		"Ein signiertes Sitzungs-Cookie, die Sitzungsdauer.":                     "Ein signiertes Session-Cookie, die Sitzungsdauer.",
		"Ein Kopf-Teil, die Kopfzeile, der Ablauf.":                              "Ein Kopf-Teil, die Kopfzeile, der Ablauf.",
	} {
		if got := Terms(in); got != want {
			t.Errorf("Terms(%q) = %q, want %q", in, got, want)
		}
		if again := Terms(Terms(in)); again != Terms(in) {
			t.Errorf("Terms not idempotent on %q: %q", in, again)
		}
	}
}

// A feminine form whose English is a neuter takes its article along where
// the case is sure, an adjective between too, and stays German where it
// is not: never "die Deployment".
func TestTermsNeuter(t *testing.T) {
	for in, want := range map[string]string{
		"Die Auslieferung läuft nachts.":                  "Das Deployment läuft nachts.",
		"Sie startet die automatische Auslieferung.":      "Sie startet das automatische Deployment.",
		"Eine neue Auslieferung, keine Auslieferung.":     "Ein neues Deployment, kein Deployment.",
		"Sie dient zur Auslieferung.":                     "Sie dient zum Deployment.",
		"Bei der Auslieferung und mit einer Auslieferung": "Beim Deployment und mit einem Deployment",
		"In der neuen Auslieferung":                       "Im neuen Deployment",
		"Sie liest die Pipeline. Build und Auslieferung":  "Sie liest die Pipeline. Build und Deployment",
		"Auslieferung über Jenkins":                       "Deployment über Jenkins",
		"- Auslieferung über Jenkins":                     "- Deployment über Jenkins",
		"## Auslieferung":                                 "## Deployment",
		"Build, Test und Auslieferung laufen.":            "Build, Test und Deployment laufen.",
		"Mit eurer Auslieferung, eure Auslieferung":       "Mit eurem Deployment, euer Deployment",
		"Die eine Auslieferung":                           "Die eine Auslieferung",
		"Die Entwicklung, Prüfung und Auslieferung":       "Die Entwicklung, Prüfung und Auslieferung",
		// A shared article, a compound's last part, markup between, a
		// capital before (an adjective or a name), a relative pronoun, a
		// code span before: German, never "die Deployment".
		"Die Entwicklung und Auslieferung laufen.":         "Die Entwicklung und Auslieferung laufen.",
		"Die Go-Auslieferung startet.":                     "Die Go-Auslieferung startet.",
		"Die **Auslieferung** läuft.":                      "Die **Auslieferung** läuft.",
		"Die „Auslieferung“ läuft.":                        "Die „Auslieferung“ läuft.",
		"Automatische Auslieferung über Jenkins.":          "Automatische Auslieferung über Jenkins.",
		"Die Automatische Auslieferung":                    "Die Automatische Auslieferung",
		"Die Pipeline, die Auslieferung und Build trennt.": "Die Pipeline, die Auslieferung und Build trennt.",
		"Startet die `deploy` Auslieferung.":               "Startet die `deploy` Auslieferung.",
		"Entwicklung und Auslieferung":                     "Entwicklung und Deployment",
		"Auslieferung per Pipeline.":                       "Deployment per Pipeline.",
		"Die Auslieferungen laufen.":                       "Die Deployments laufen.",
		"Die Auslieferungs-Pipeline baut.":                 "Die Deployment-Pipeline baut.",
		// A genitive or dative the code cannot tell apart, an adjective
		// without an article (a strong ending): German.
		"Während der Auslieferung wartet sie.": "Während der Auslieferung wartet sie.",
		"Der Auslieferung fehlt ein Schritt.":  "Der Auslieferung fehlt ein Schritt.",
		"Sie sorgt für schnelle Auslieferung.": "Sie sorgt für schnelle Auslieferung.",
		"`Auslieferung` und Auslieferung.java": "`Auslieferung` und Auslieferung.java",
		// A comma before "die": an article where no verb can follow before
		// the block ends, nouns, joins and articles alone; a relative
		// pronoun's clause has its verb last.
		"Build, die Auslieferung und Tests.":                     "Build, das Deployment und Tests.",
		"Sie plant Build, die Auslieferung und die Tests.\n":     "Sie plant Build, das Deployment und die Tests.\n",
		"Drei Schritte: Build, Test, die Auslieferung!":          "Drei Schritte: Build, Test, das Deployment!",
		"Sie fragt, welche Auslieferung?":                        "Sie fragt, welches Deployment?",
		"Build, die Auslieferung und Tests":                      "Build, das Deployment und Tests",
		"Build, die Auslieferung und Tests.\n\nDanach baut sie.": "Build, das Deployment und Tests.\n\nDanach baut sie.",
		"- Build, die Auslieferung und Tests\n- Lint":            "- Build, das Deployment und Tests\n- Lint",
		"Build, die Auslieferung, Tests\n## Lint":                "Build, das Deployment, Tests\n## Lint",
		"Build, die Auslieferung und die einzelnen Tests.":       "Build, die Auslieferung und die einzelnen Tests.",
		"Build, die Auslieferung und Tests einen.":               "Build, die Auslieferung und Tests einen.",
		// A sentence end inside a block may be an abbreviation's, a name's,
		// an image's; the verb may follow on the next line or in code.
		"Build, die Auslieferung.\nTests laufen.":                         "Build, die Auslieferung.\nTests laufen.",
		"Build, die Auslieferung. Tests laufen.":                          "Build, die Auslieferung. Tests laufen.",
		"Sie fragt, welche Auslieferung; dann baut sie.":                  "Sie fragt, welche Auslieferung; dann baut sie.",
		"Die Pipeline, die Auslieferung und Tests Nr. `42` baut.":         "Die Pipeline, die Auslieferung und Tests Nr. `42` baut.",
		"Die Pipeline, die Auslieferung und Tests Nr.\n3 baut.":           "Die Pipeline, die Auslieferung und Tests Nr.\n3 baut.",
		"Die Pipeline, die Auslieferung und Tests Nr.\n- baut.":           "Die Pipeline, die Auslieferung und Tests Nr.\n- baut.",
		"1. Build, die Auslieferung und Tests\n2. Lint":                   "1. Build, das Deployment und Tests\n2. Lint",
		"Die Firma, die Auslieferung und Lager St. Gallen betreibt.":      "Die Firma, die Auslieferung und Lager St. Gallen betreibt.",
		"Die Pipeline, die Auslieferung ![Bild](x.png) zeigt.":            "Die Pipeline, die Auslieferung ![Bild](x.png) zeigt.",
		"Die Pipeline, die Auslieferung und Yahoo! Mail nutzt.":           "Die Pipeline, die Auslieferung und Yahoo! Mail nutzt.",
		"Die Pipeline,\ndie Auslieferung braucht.":                        "Die Pipeline,\ndie Auslieferung braucht.",
		"Die Pipeline, \n  die Auslieferung und Build trennt.":            "Die Pipeline, \n  die Auslieferung und Build trennt.",
		"Die Pipeline,\ndie Auslieferung.":                                "Die Pipeline,\ndas Deployment.",
		"Die Pipeline, die Auslieferung braucht.":                         "Die Pipeline, die Auslieferung braucht.",
		"Build, die Auslieferung und Tests laufen.":                       "Build, die Auslieferung und Tests laufen.",
		"Build, die Auslieferung und neue Tests.":                         "Build, die Auslieferung und neue Tests.",
		"Die Pipeline, die Auslieferung und Tests\n- wie geplant - baut.": "Die Pipeline, die Auslieferung und Tests\n- wie geplant - baut.",
		"Build, die Auslieferung\nbraucht.":                               "Build, die Auslieferung\nbraucht.",
		"Die Pipeline, die Auslieferung `startet`.":                       "Die Pipeline, die Auslieferung `startet`.",
		"Die Pipeline, die Auslieferung **startet**.":                     "Die Pipeline, die Auslieferung **startet**.",
		"Die Pipeline, die Auslieferung und Test-Läufe.":                  "Die Pipeline, die Auslieferung und Test-Läufe.",
		"Die Pipeline, die Auslieferung und Tests (CI) baut.":             "Die Pipeline, die Auslieferung und Tests (CI) baut.",
		"Die Pipeline, die Auslieferung und Tests Nr. 3 baut":             "Die Pipeline, die Auslieferung und Tests Nr. 3 baut",
		"Die Pipeline, die Auslieferung und Tests: alle.":                 "Die Pipeline, die Auslieferung und Tests: alle.",
		"Die Pipeline, die Auslieferung und B) Tests trennt.":             "Die Pipeline, die Auslieferung und B) Tests trennt.",
		"Die Pipeline, die Auslieferung und Tests usw. baut.":             "Die Pipeline, die Auslieferung und Tests usw. baut.",
		// An adjective between could be a verb ("die startete Auslieferung").
		"Build, die neue Auslieferung.": "Build, die neue Auslieferung.",
		// A code span before the comma: the pronoun still has a noun before it.
		"Die Pipeline `ci`, die Auslieferung und Build trennt.": "Die Pipeline `ci`, die Auslieferung und Build trennt.",
	} {
		got := Terms(in)
		if got != want {
			t.Errorf("Terms(%q) = %q, want %q", in, got, want)
		}
		if again := Terms(got); again != got {
			t.Errorf("Terms not idempotent on %q: %q", in, again)
		}
	}
}

// Every word before a noun as its one entry says: a bare noun after it or
// German, a feminine "der" or "einer" after it made a dative or German,
// "der" merged where German merges it.
func TestTermsPreps(t *testing.T) {
	check := func(in, want string) {
		t.Helper()
		if got := Terms(in); got != want {
			t.Errorf("Terms(%q) = %q, want %q", in, got, want)
		}
	}
	for w, p := range preps {
		if p.into != "" && !p.dative {
			t.Errorf("%q merges with the dative's \"dem\" but takes no dative", w)
		}
		in := "Sie liest " + w + " Auslieferung."
		if p.bare {
			check(in, "Sie liest "+w+" Deployment.")
		} else {
			check(in, in)
		}
		der, einer := "Sie liest "+w+" der Auslieferung.", "Sie liest "+w+" einer Auslieferung."
		switch {
		case p.into != "":
			check(der, "Sie liest "+p.into+" Deployment.")
			check(einer, "Sie liest "+w+" einem Deployment.")
		case p.dative:
			check(der, "Sie liest "+w+" dem Deployment.")
			check(einer, "Sie liest "+w+" einem Deployment.")
		default:
			check(der, der)
			check(einer, einer)
		}
	}
	// A genitive's preposition is in no entry: German.
	check("Wegen der Auslieferung", "Wegen der Auslieferung")
	check("Während der Auslieferung", "Während der Auslieferung")
	// Capitalised, the merge keeps the capital.
	check("Bei der Auslieferung", "Beim Deployment")
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

// Every form begins with a stem, a tail form's with its hyphen, or Terms
// never looks for it.
func TestTermsStems(t *testing.T) {
	for de, tm := range terms {
		in := de
		if tm.tail {
			in = "-" + de
		}
		ok := false
		for _, st := range stems {
			ok = ok || strings.HasPrefix(in, st)
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
