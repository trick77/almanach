// Every string the UI shows, in one place. German, Swiss spelling (ss,
// never the sharp s), generic masculine. There is no second language: the
// readers are German speaking, and the wiki itself is German.

import type { SkippedPart, SkipReason } from "./api";

export const S = {
  app: "Almanach",
  nav: {
    label: "Seiten",
    projects: "Projekte",
    sources: "Quellen",
    faq: "FAQ",
  },
  common: {
    loading: "Lädt …",
    failed: (what: string) => `${what} konnte nicht geladen werden.`,
    none: "keine",
    never: "nie",
    sep: "·",
  },
  projects: {
    title: "Projekte",
    lede: "Was Almanach über jedes Projekt weiss: pro Repository und pro Projekt eine Beschreibung, aus dem Code gelesen und mit den Notizen gemergter Pull Requests fortgeschrieben, nie von Hand.",
    what: "die Projekte",
    empty:
      "Noch kein Projekt: die erste Notiz kommt mit dem ersten gemergten Pull Request eines Teams mit almanach-Block.",
    team: "Team",
    repos: (n: number) => (n === 1 ? "1 Repository" : `${n} Repositories`),
    notes: (n: number) => (n === 1 ? "1 Notiz" : `${n} Notizen`),
    revision: (n: number) => `Revision ${n}`,
    noDescription: "noch keine Beschreibung",
    firstDescription:
      "Die erste Beschreibung schreibt Almanach aus dem Code, sobald der Klon bereit ist, oder der nächste gemergte Pull Request.",
    lastMerge: "letzter Merge",
    problems: (n: number) =>
      n === 0
        ? "keine Problemzonen"
        : n === 1
          ? "1 Problemzone"
          : `${n} Problemzonen`,
  },
  project: {
    what: "das Projekt",
    breadcrumbRoot: "Projekte",
    noDescription: "Noch keine Beschreibung.",
    noDescriptionHint:
      "Die erste Beschreibung entsteht, wenn Almanach ein Repository dieses Projekts aus dem Code beschreibt oder ein Pull Request gemergt wird.",
    descriptionRevision: (n: number) => `Beschreibung Revision ${n}`,
    repos: "Repositories",
    repoHeaders: ["Repository", "Beschreibung", "Notizen", "Letzter Merge"],
    noRepos: "Noch kein Repository.",
    problems: "Problemzonen",
    problemsWindow: (days: number) => `letzte ${days} Tage`,
    problemHeaders: [
      "Repository",
      "Bereich",
      "Komponente",
      "Bugfixes",
      "Häufigste Ursache",
      "Issues / Hinweise",
      "Letzter Fix",
    ],
    noProblems: "Keine Bugfixes im Zeitfenster.",
    rootArea: "(Wurzel)",
    recentNotes: "Letzte Notizen",
    noNotes: "Noch keine Notiz.",
    map: "Systemkarte",
    noMap:
      "Noch keine Systemkarte: sie entsteht beim nächsten Run, der dieses Projekt aus dem Code beschreibt.",
  },
  diagram: {
    titles: {
      flow: "Ablaufdiagramm",
      sequence: "Sequenzdiagramm",
      er: "Datenmodell",
      map: "Systemkarte",
      repoMap: "Aufbau",
    },
    // The map's zones by id: the backend allows these four and no other.
    zones: {
      clients: "Clients",
      repositories: "Repositories",
      stores: "Datenspeicher",
      external: "Externe Systeme",
    } as Record<string, string>,
    // A repository map's zones by id, the same rule.
    repoZones: {
      entry: "Eingang",
      parts: "Teile",
      stores: "Datenspeicher",
      external: "Externe Systeme",
    } as Record<string, string>,
    pending: "Diagramm wird gezeichnet …",
    failed: "Das Diagramm konnte nicht gezeichnet werden.",
    full: "Vollansicht",
    fullOf: (title: string) => `${title}: Vollansicht`,
    download: "SVG herunterladen",
    downloadOf: (title: string) => `${title}: SVG herunterladen`,
    close: "Schliessen",
    repoLink: (slug: string) => `Repository ${slug} öffnen`,
  },
  // The tabs of the project and the repository page, and their hash: the
  // address keeps the tab, so a shared link opens it.
  tabs: {
    label: "Ansicht",
    overview: "Übersicht",
    description: "Beschreibung",
    history: "Verlauf",
  },
  rail: {
    label: "Inhalt",
    search: "Durchsuchen",
    searchLabel: "Beschreibung durchsuchen",
    hits: (n: number, of: number, kinds: number) =>
      `${n} von ${of} ${of === 1 ? "Eintrag" : "Einträgen"} ${S.common.sep} ${kinds} ${kinds === 1 ? "Art" : "Arten"}`,
    noHits: "keine Treffer",
    summary: "Überblick",
    project: "Projekt",
  },
  repo: {
    what: "das Repository",
    project: "Projekt",
    map: "Aufbau",
    revisions: "Revisionen",
    revisionFrom: "aus",
    current: "aktuell",
    statements: (n: number) => (n === 1 ? "1 Aussage" : `${n} Aussagen`),
    chars: (n: number) => `${fmtInt(n)} Zeichen`,
    noRevisions: "Noch keine Revision.",
    notes: "Notizen",
  },
  kinds: {
    component: "Komponenten",
    rule: "Regeln",
    invariant: "Invarianten",
    entity: "Entitäten",
    decision: "Entscheidungen",
    extension: "Erweiterungen",
    pitfall: "Fallstricke",
  },
  // The project page's names where they differ: its entities are the ones
  // that cross repositories, who owns them and how their ids travel.
  projectKinds: {
    entity: "Geteilte Daten",
  } as Partial<Record<string, string>>,
  noteHeaders: ["PR", "Titel", "Art", "Einträge", "Issues / Hinweise", "Merge"],
  changeKind: {
    bugfix: "Bugfix",
    feature: "Feature",
    refactor: "Refactoring",
    chore: "Chore",
    unknown: "unbekannt",
  },
  causeKind: {
    logic: "Logik",
    concurrency: "Nebenläufigkeit",
    config: "Konfiguration",
    input_validation: "Eingabeprüfung",
    integration: "Integration",
    missing_test: "Fehlender Test",
    other: "Andere",
  },
  note: {
    what: "die Notiz",
    ticket: "Ticket",
    merge: "Merge",
    summary: "Zusammenfassung",
    noSummary:
      "Die Notiz hat keine Zusammenfassung: der PR hat nichts über das System gelehrt.",
    bugfix: "Bugfix",
    symptom: "Symptom",
    cause: "Ursache",
    causeKind: "Ursachenklasse",
    noneOfKind: "keine",
    facts: "Fakten",
    rounds: "Review-Runden",
    posted: "Issues / Hinweise gepostet",
    reviewedCommit: "Reviewter Commit",
    sourceCommit: "Source-Commit",
    mergeCommit: "Merge-Commit",
    model: "Modell",
    received: "Empfangen",
    consolidated: "Konsolidiert",
    notYet: "noch nicht",
    call: "Notiz-Aufruf",
    callHint: "was noergler für diese Notiz gemeldet hat",
    noCall: "noergler hat für diese Notiz keinen Aufruf gemeldet.",
    cacheHit: "Cache-Treffer",
    cacheHitNote: (read: number, input: number) =>
      `${fmtInt(read)} von ${fmtInt(input)} Input-Tokens`,
    output: "Output",
    outputNote: (reasoning: number | null) =>
      reasoning === null ? "Tokens" : `davon ${fmtInt(reasoning)} Reasoning`,
    cost: "Kosten",
    costNote: "diese Runde",
    allRounds: "Alle Runden",
    allRoundsNote: (calls: number, tokens: number) =>
      `${calls} ${calls === 1 ? "Aufruf" : "Aufrufe"} ${S.common.sep} ${fmtInt(tokens)} Tokens`,
    unpriced: "nicht bepreist",
    files: "Dateien",
  },
  sources: {
    title: "Quellen",
    lede: "Die Projekte, deren Code Almanach selbst klont: ein Bitbucket-Projekt, dessen Repositories aus der API gelesen werden, oder ein statischer Ordner mit von Hand eingetragenen Repositories. Die Klone sind die Grundlage, die später beschrieben und mit jedem gemergten Pull Request fortgeschrieben wird.",
    what: "die Quellen",
    whatOne: "die Quelle",
    empty: "Noch keine Quelle.",
    disabled:
      "Quellen sind auf dieser Instanz nicht konfiguriert (BACKEND_REPOS_DIR).",
    headers: [
      "Slug",
      "Art",
      "Host",
      "Token",
      "Repos",
      "Zustand",
      "Letzter Run",
    ],
    kind: { bitbucket: "bitbucket", static: "statisch" } as Record<
      string,
      string
    >,
    kindLabel: {
      bitbucket: "Bitbucket-Projekt",
      // No-break spaces: a narrow form wraps before the bracket, never inside it.
      static: "Statischer Ordner (z.\u00a0B.\u00a0GitHub)",
    } as Record<string, string>,
    kindHint: {
      bitbucket:
        "Die Repositories kommen aus der Bitbucket-API und werden bei jedem Run neu gelistet.",
      static:
        "Ein Ordner, in den du Repositories von Hand einträgst: Slug und Clone-URL, zum Beispiel die Repos einer GitHub-Organisation. Das Token dafür kommt ins Feld unten.",
    } as Record<string, string>,
    tokenStored: "Hinterlegt",
    tokenNone: "Öffentlich",
    tokenAs: (auth: string, user: string) =>
      auth === "basic" ? `Basic als ${user || "x-access-token"}` : "Bearer",
    listingFailed: "Listing fehlgeschlagen",
    listedAt: "gelistet",
    neverListed: "noch nie gelistet",
    manual: "Repositories von Hand eingetragen",
    counts: {
      ready: (n: number) => `${n} bereit`,
      active: (n: number) => `${n} läuft`,
      failed: (n: number) => `${n} fehlgeschlagen`,
      missing: (n: number) => `${n} verschwunden`,
      none: "Keine Repositories",
    },
    newSource: "Neue Quelle",
    form: {
      kind: "Art",
      slug: "Slug",
      slugHint:
        "Buchstaben, Ziffern, Punkt, Strich, Unterstrich, Tilde. Lässt sich später umbenennen.",
      host: "Bitbucket-Host",
      hostHint:
        "Nur https. Die Clone-URLs der gelisteten Repositories müssen auf diesen Host zeigen.",
      token: "Personal Access Token",
      optional: "(optional)",
      tokenPlaceholder: "Leer lassen für öffentliche Repositories",
      tokenHint: {
        bitbucket:
          "Nur nötig für private Repositories, ein privates Bitbucket-Projekt oder einen Host, der die API nicht anonym beantwortet. Das Token listet das Projekt über die API und klont seine Repositories.",
        static:
          "Nur nötig für private Repositories. Ein Token für den ganzen Ordner: es gilt für jedes Repository darin. Für GitHub ein klassisches Token mit Scope «repo» oder ein Fine-grained-Token mit «Contents: read».",
      } as Record<string, string>,
      tokenAs: "Token senden als",
      bearerHint:
        "Bearer: das Token als Authorization-Header, kein Benutzername nötig. Für Bitbucket Data Center.",
      basicHint:
        "Basic: Benutzername und Token als Basic-Auth-Header. Für GitHub.",
      user: "Benutzername",
      userHint:
        "GitHub ignoriert ihn; Bitbucket prüft ihn (Projekt- und Repo-Token: x-token-auth, persönliche Token: der eigene Name).",
      create: "Anlegen und synchronisieren",
      cancel: "Abbrechen",
      save: "Speichern",
      replace: "Ersetzen",
      clear: "Löschen",
      storedHint:
        "Ersetzen überschreibt das gespeicherte Token, Löschen macht die Quelle öffentlich.",
      newToken: "Neues Token",
      saving: "Speichert …",
    },
    tiles: {
      repos: "Repositories",
      reposNote: {
        bitbucket: "aus der API",
        static: "von Hand eingetragen",
      } as Record<string, string>,
      ready: "Bereit",
      lastSync: "Letzter Sync",
      running: "läuft gerade",
      disk: "Auf der Platte",
    },
    actions: {
      sync: "Synchronisieren",
      syncRunning: "Synchronisieren läuft gerade.",
      queued: "Angestossen …",
      addRepo: "Repository hinzufügen",
      access: { bitbucket: "Host und Token", static: "Token" } as Record<
        string,
        string
      >,
      remove: "Quelle entfernen",
      rename: "Umbenennen",
    },
    renameForm: {
      slug: "Neuer Slug",
      hint: "Auch nur die Gross- und Kleinschreibung. Seiten, Notizen und Klone bleiben, der alte Slug führt weiter hierher. Ein Slug, den ein anderes Projekt je hatte, ist vergeben, in jeder Schreibweise.",
      save: "Umbenennen",
    },
    renameRepoForm: {
      title: (slug: string) => `${slug} umbenennen`,
      slug: "Neuer Slug",
      slugHint:
        "Auch nur die Gross- und Kleinschreibung. Beschreibung, Notizen und Runs bleiben, der alte Slug führt weiter hierher. Ein Slug, den ein anderes Repository je hatte, ist vergeben.",
      url: "Neue Clone-URL",
      urlPlaceholder: "(bleibt)",
      urlHint: "Nur https. Eine neue URL holt den Klon neu.",
      save: "Umbenennen",
    },
    confirmRemove: (key: string, repos: number, dir: string) =>
      `Quelle ${key} entfernen? Löscht die Quelle, ihre ${repos} Repository-Einträge und den Ordner ${dir}. Beschreibungen aus Pull-Request-Notizen bleiben; sie hängen am Projekt, nicht an der Quelle.`,
    confirmRemoveRepo: (slug: string, dir: string) =>
      `Repository ${slug} entfernen? Löscht den Eintrag und den Klon unter ${dir}. Der Eintrag kann jederzeit neu angelegt werden; der Klon wird dann neu geholt.`,
    yesRemove: "Ja, entfernen",
    repos: "Repositories",
    reposNote: {
      bitbucket: "Reihenfolge der API, archivierte übersprungen",
      static: "alphabetisch",
    } as Record<string, string>,
    polling: "Seite aktualisiert alle 3 s, solange etwas läuft",
    repoHeaders: [
      "Repository",
      "Branch",
      "Head",
      "Zustand",
      "Letzter Sync",
      "Beschreibung",
      "",
    ],
    noRepos: {
      bitbucket:
        "Noch kein Repository: das erste Listing läuft mit dem nächsten Zyklus.",
      static: "Noch kein Repository. Trag eines ein.",
    } as Record<string, string>,
    listedOnly:
      "Bei einer Bitbucket-Quelle werden Repositories nicht von Hand hinzugefügt: die Liste ist die der API. Ein Repository, das die API nicht mehr liefert, wird als «verschwunden» markiert und sein Klon bleibt, bis du ihn entfernst.",
    state: {
      pending: "Ausstehend",
      syncing: "Läuft",
      ready: "Bereit",
      failed: "Fehlgeschlagen",
      missing: "Verschwunden",
    } as Record<string, string>,
    repoActions: {
      sync: "Synchronisieren",
      retry: "Erneut",
      remove: "Entfernen",
      removeClone: "Klon entfernen",
      rename: "Umbenennen",
    },
    addForm: {
      title: "Repository hinzufügen",
      slug: "Slug",
      slugHint:
        "Buchstaben, Ziffern, Punkt, Strich, Unterstrich. Lässt sich später umbenennen.",
      branch: "Branch",
      branchPlaceholder: "(Default-Branch des Remotes)",
      branchHint:
        "Leer: wird mit ls-remote vom Remote gelesen und hier eingetragen.",
      url: "Clone-URL",
      urlHint: "Nur https.",
      add: "Hinzufügen und klonen",
    },
    listingError: "Letztes Listing fehlgeschlagen:",
    listingErrorHint:
      "Die Repository-Liste ist die des letzten erfolgreichen Listings; bekannte Repositories werden weiter synchronisiert.",
  },
  baseline: {
    chip: (sha: string, repo?: string) =>
      repo ? `Basis ${repo} ${sha}` : `Basis ${sha}`,
    chipTitle: (sha: string, date: string) =>
      date === ""
        ? `Basis bei Commit ${sha}`
        : `Basis bei Commit ${sha}, ${date}`,
    checked: (sha: string) => `Geprüft ${sha}`,
    checkedTitle: (sha: string) => `Gegen den Code bei Commit ${sha} geprüft`,
    state: {
      none: "Noch nicht",
      queued: "Wartet",
      cataloguing: "Katalog",
      describing: (step: number, steps: number) =>
        `Beschreibt ${step}/${steps}`,
      project: "Projekt",
      // The run's last step, in state project once the project step is over.
      repoMap: "Aufbau",
      done: "Beschrieben",
      partial: "Teilweise",
      failed: "Fehlgeschlagen",
    },
    at: (sha: string) => `Bei ${sha}`,
    soFar: "bisher",
    // Parts, not topics: the overview and the project step count too.
    skipped: (n: number) =>
      n === 1 ? "1 Teil übersprungen" : `${n} Teile übersprungen`,
    // A skipped part by name: a subject is its own, the overview and the
    // project step are these.
    skippedPart: {
      overview: "Überblick",
      project: "Projekt",
    } satisfies Record<Exclude<SkippedPart["part"], "subject">, string>,
    // One reason's line: its first names, the rest counted.
    skipLine: (reason: string, names: string[], more: number) =>
      `${reason}: ${names.join(", ")}${more > 0 ? ` und ${more} weitere` : ""}`,
    // Why a part was left out, one line per reason in front of its parts;
    // a code the worker adds without copy here fails the typecheck. An
    // error says so; a run's own budget is no error.
    skipReason: {
      gone: "Nicht mehr im Katalog",
      calls: "Aufrufe des Laufs aufgebraucht",
      bytes: "Lesebudget des Laufs überschritten",
      // TRANSITIONAL(ceilings): Remove when no stored run plan carries it.
      ceiling: "An der früheren Seitengrenze",
      failed: "Fehler: Modellaufruf fehlgeschlagen",
      refused: "Fehler: Antwort regelwidrig, verworfen",
      // TRANSITIONAL(ceilings): Remove when no stored run plan carries it.
      over_ceiling: "Über der früheren Seitengrenze verworfen",
      no_page: "Projekt ohne Seite",
      "": "Grund nicht erfasst",
    } satisfies Record<SkipReason, string>,
    // Under the skipped parts: when they are read again. The poller retries
    // a done run once; a retry's own leftovers wait for a button.
    retryAuto: "Wird nach etwa einem Tag von selbst erneut gelesen.",
    retryManual: "Schon einmal wiederholt: Aktualisieren liest sie erneut.",
    failedRun: "Beschreibung fehlgeschlagen:",
    upToDate:
      "Der Head ist schon beschrieben: Neu beschreiben liest alles erneut.",
    described: (n: number) => `${n} beschrieben`,
    actions: {
      describe: "Beschreiben",
      reconcile: "Aktualisieren",
      full: "Neu beschreiben",
    },
  },
  faq: {
    title: "FAQ",
    what: "Was ist Almanach?",
    whatText:
      "Almanach ist das lebende Wiki, das noergler beim Reviewen füttert: nach jedem Review schreibt das Modell eine Notiz, was der Pull Request über das Repository lehrt, und beim Merge kommt sie hierher. Almanach fügt jede Notiz in die Beschreibung des Repositorys und des Projekts ein, als Dokumentation mit Quellen, nie von Hand geschrieben.",
    who: "Wer schreibt hier?",
    whoText:
      "Niemand von Hand. Eine Beschreibung schreibt Almanach aus dem Code (beim ersten Klon und einmal im Monat) und aus jedem gemergten Pull Request fort; was falsch ist, korrigiert der nächste Run oder Pull Request. Jede Revision bleibt erhalten.",
    sources: "Woher kommt eine Aussage?",
    sourcesText:
      "Jede Aussage trägt die Pull Requests, aus denen sie gelernt oder zuletzt geändert wurde, als Chips, und den Commit, bei dem Almanach sie aus dem Code gelesen hat (Basis). «Geprüft» nennt den Commit, gegen dessen Code sie zuletzt bestätigt wurde. Die Quellen setzt der Code, nie das Modell.",
    clones: "Was sind die Quellen?",
    clonesText:
      "Die Projekte, deren Code Almanach selbst klont: ein Bitbucket-Projekt, dessen Repositories aus der API gelesen werden, oder ein statischer Ordner mit von Hand eingetragenen Repositories. Almanach liest jeden Klon und schreibt daraus die Grundbeschreibung des Repositorys; ab dann schreiben die Notizen der Pull Requests sie fort, und einmal im Monat liest Almanach, was sich im Code geändert hat. Notizen für Repositories, die keine Quelle sind, werden ignoriert.",
  },
  auth: {
    checking: "Sitzung wird geprüft …",
    redirecting: "Weiterleitung zur Anmeldung …",
    signedOut:
      "Abgemeldet. Die Sitzung beim Identitätsanbieter läuft weiter; wer sie beenden will, meldet sich auch dort ab.",
    failed:
      "Die Anmeldung konnte nicht abgeschlossen werden. Bitte erneut versuchen; schlägt sie wieder fehl, steht der Grund im Server-Log.",
    unchecked: (status: number) =>
      `Die Sitzung konnte nicht geprüft werden (HTTP ${status}).`,
    unreadable:
      "Die Sitzung konnte nicht geprüft werden: die Antwort des Servers war unlesbar.",
    offline:
      "Die Sitzung konnte nicht geprüft werden: der Server ist nicht erreichbar.",
    misconfigured:
      "Die Anmeldung ist falsch eingerichtet: der Identitätsanbieter passt nicht zur Konfiguration. Der Grund steht im Server-Log; nach der Korrektur und einem Neustart erneut versuchen.",
    signOutFailed: "Abmelden fehlgeschlagen, bitte erneut versuchen.",
    unavailable:
      "Die Anmeldung ist im Moment nicht möglich: der Identitätsanbieter antwortet nicht. Almanach versucht es laufend weiter; bitte später erneut versuchen.",
    loop: "Die Anmeldung hat gerade eben keine Sitzung ergeben. Bitte erneut anmelden; geschieht das wieder, steht der Grund im Server-Log.",
    signIn: "Anmelden",
    signOut: "Abmelden",
    retry: "Erneut versuchen",
  },
} as const;

/** fmtInt renders a count the Swiss way: a thin space every three digits.
 *  Done by hand: Intl's de-CH separator differs between ICU builds (a
 *  straight apostrophe in node, a curly one in browsers). */
export function fmtInt(n: number): string {
  const sign = n < 0 ? "-" : "";
  const digits = String(Math.trunc(Math.abs(n)));
  return sign + digits.replace(/\B(?=(\d{3})+$)/g, "\u202f");
}
