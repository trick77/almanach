// Every string the UI shows, in one place. German, Swiss spelling (ss,
// never the sharp s), generic masculine. There is no second language: the
// readers are German speaking, and the wiki itself is German.

export const S = {
  app: "Almanach",
  nav: { label: "Seiten", projects: "Projekte", faq: "FAQ" },
  common: {
    loading: "Lädt …",
    failed: (what: string) => `${what} konnte nicht geladen werden.`,
    none: "keine",
    never: "nie",
    sep: "·",
  },
  projects: {
    title: "Projekte",
    lede: "Was noergler beim Reviewen über jedes Projekt gelernt hat: pro Repository und pro Projekt eine Beschreibung, aus den Notizen gemergter Pull Requests, nie von Hand geschrieben.",
    what: "die Projekte",
    empty:
      "Noch kein Projekt: die erste Notiz kommt mit dem ersten gemergten Pull Request eines Teams mit almanach-Block.",
    team: "Team",
    repos: (n: number) => (n === 1 ? "1 Repository" : `${n} Repositories`),
    notes: (n: number) => (n === 1 ? "1 Notiz" : `${n} Notizen`),
    revision: (n: number) => `Revision ${n}`,
    noDescription: "noch keine Beschreibung",
    firstDescription:
      "Die erste Beschreibung entsteht mit dem nächsten gemergten Pull Request.",
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
      "Die erste Beschreibung entsteht mit dem nächsten gemergten Pull Request dieses Projekts.",
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
  faq: {
    title: "FAQ",
    what: "Was ist Almanach?",
    whatText:
      "Almanach ist das lebende Wiki, das noergler beim Reviewen füttert: nach jedem Review schreibt das Modell eine Notiz, was der Pull Request über das Repository lehrt, und beim Merge kommt sie hierher. Almanach fügt jede Notiz in die Beschreibung des Repositorys und des Projekts ein, als Dokumentation mit Quellen, nie von Hand geschrieben.",
    who: "Wer schreibt hier?",
    whoText:
      "Niemand. Eine Beschreibung ändert sich nur, wenn ein gemergter Pull Request sie ändert; der nächste, der es richtig macht, korrigiert sie. Jede Revision bleibt erhalten.",
    sources: "Woher kommt eine Aussage?",
    sourcesText:
      "Jede Aussage trägt die Pull Requests, aus denen sie gelernt oder zuletzt geändert wurde, als Chips. Die Quellen setzt der Code, nie das Modell.",
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
