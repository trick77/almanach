// Every string the UI shows, in one place. German, Swiss spelling (ss,
// never the sharp s), generic masculine. There is no second language: the
// readers are German speaking, and the wiki itself is German.

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
    description: "Beschreibung",
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
  },
  repo: {
    what: "das Repository",
    project: "Projekt",
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
  },
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
      "Quellen sind auf dieser Instanz nicht konfiguriert (ALMANACH_REPOS_DIR).",
    headers: [
      "Slug",
      "Art",
      "Host",
      "Token",
      "Repos",
      "Zustand",
      "Letzter Lauf",
    ],
    kind: { bitbucket: "bitbucket", static: "statisch" } as Record<
      string,
      string
    >,
    kindLabel: {
      bitbucket: "Bitbucket-Projekt",
      static: "Statischer Ordner",
    } as Record<string, string>,
    kindHint: {
      bitbucket:
        "Die Repositories kommen aus der Bitbucket-API und werden bei jedem Lauf neu gelistet.",
      static:
        "Ein Ordner, in den du Repositories von Hand einträgst: Slug und Clone-URL, zum Beispiel die Repos einer GitHub-Organisation.",
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
        "Wird der Ordnername unter dem Basisordner. Buchstaben, Ziffern, Punkt, Strich, Unterstrich, Tilde.",
      host: "Bitbucket-Host",
      hostHint:
        "Nur https. Die Clone-URLs der gelisteten Repositories müssen auf diesen Host zeigen.",
      token: "Personal Access Token",
      optional: "(optional)",
      tokenPlaceholder: "Leer lassen für öffentliche Repositories",
      tokenHint:
        "Nur nötig für private Repositories oder ein privates Bitbucket-Projekt. Verschlüsselt gespeichert, nie zurückgegeben, nie geloggt, landet nie in .git/config.",
      tokenAs: "Token senden als",
      bearerHint:
        "Bearer: das Token als Authorization-Header, kein Benutzername nötig. Für Bitbucket Data Center die sichere Wahl.",
      basicHint:
        "Basic: Benutzername und Token in der URL für genau diesen einen git-Aufruf. Für GitHub die übliche Wahl.",
      user: "Benutzername",
      userHint:
        "GitHub ignoriert ihn; Bitbucket prüft ihn (Projekt- und Repo-Token: x-token-auth, persönliche Token: der eigene Name).",
      create: "Anlegen und synchronisieren",
      cancel: "Abbrechen",
      save: "Speichern",
      replace: "Ersetzen",
      clear: "Löschen",
      storedHint:
        "Das gespeicherte Token wird nie angezeigt. Ersetzen überschreibt es, Löschen macht die Quelle öffentlich.",
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
      queued: "Angestossen …",
      addRepo: "Repository hinzufügen",
      access: { bitbucket: "Host und Token", static: "Token" } as Record<
        string,
        string
      >,
      remove: "Quelle entfernen",
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
    },
    addForm: {
      title: "Repository hinzufügen",
      slug: "Slug",
      slugHint: (dir: string) => `Der Ordnername unter ${dir}.`,
      branch: "Branch",
      branchPlaceholder: "(Default-Branch des Remotes)",
      branchHint:
        "Leer: wird mit ls-remote vom Remote gelesen und hier eingetragen.",
      url: "Clone-URL",
      urlHint:
        "Nur https. Das Token der Quelle wird beim Klonen mitgegeben, nie in die URL geschrieben.",
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
      done: "Beschrieben",
      partial: "Teilweise",
      failed: "Fehlgeschlagen",
    },
    at: (sha: string) => `Bei ${sha}`,
    soFar: "bisher",
    skipped: (n: number) =>
      n === 1 ? "1 Thema übersprungen" : `${n} Themen übersprungen`,
    failedRun: "Beschreibung fehlgeschlagen:",
    upToDate:
      "Der Head ist schon beschrieben: Neu beschreiben liest alles erneut.",
    described: (n: number) => `${n} beschrieben`,
    actions: {
      describe: "Beschreiben",
      reconcile: "Aktualisieren",
      full: "Neu beschreiben",
    },
    auto: "Automatisch beschreiben",
    autoHint:
      "Ein Repository beim ersten Klon (ein Fehlschlag einen Tag später erneut), danach monatlich, wenn sich der Head bewegt hat; gelesen wird nur, was sich seit der letzten Beschreibung geändert hat. Aus: nur über die Knöpfe, für alle Repositories dieser Quelle.",
  },
  faq: {
    title: "FAQ",
    what: "Was ist Almanach?",
    whatText:
      "Almanach ist das lebende Wiki, das noergler beim Reviewen füttert: nach jedem Review schreibt das Modell eine Notiz, was der Pull Request über das Repository lehrt, und beim Merge kommt sie hierher. Almanach fügt jede Notiz in die Beschreibung des Repositorys und des Projekts ein, als Dokumentation mit Quellen, nie von Hand geschrieben.",
    who: "Wer schreibt hier?",
    whoText:
      "Niemand von Hand. Eine Beschreibung schreibt Almanach aus dem Code (beim ersten Klon und einmal im Monat) und aus jedem gemergten Pull Request fort; was falsch ist, korrigiert der nächste Lauf oder Pull Request. Jede Revision bleibt erhalten.",
    sources: "Woher kommt eine Aussage?",
    sourcesText:
      "Jede Aussage trägt die Pull Requests, aus denen sie gelernt oder zuletzt geändert wurde, als Chips, und den Commit, bei dem Almanach sie aus dem Code gelesen hat (Basis). «Geprüft» nennt den Commit, gegen dessen Code sie zuletzt bestätigt wurde. Die Quellen setzt der Code, nie das Modell.",
    clones: "Was sind die Quellen?",
    clonesText:
      "Die Projekte, deren Code Almanach selbst klont: ein Bitbucket-Projekt, dessen Repositories aus der API gelesen werden, oder ein statischer Ordner mit von Hand eingetragenen Repositories. Almanach liest jeden Klon und schreibt daraus die Grundbeschreibung des Repositorys; ab dann schreiben die Notizen der Pull Requests sie fort, und einmal im Monat liest Almanach, was sich im Code geändert hat. Notizen für Repositories, die keine Quelle sind, werden ignoriert.",
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
