// Every string the UI shows, in one place. German, Swiss spelling (ss,
// never the sharp s), generic masculine. There is no second language: the
// readers are German speaking, and the wiki itself is German. A software
// word a German developer says in English stays English: Repository, Queue,
// Run, Timeout, Session (user, 2026-11-08). Abhängigkeit stays German, as on
// the pages (user, 2026-11-10).

import type {
  BriefState,
  Kind,
  QueueRun,
  SkippedPart,
  SkipReason,
  StepKind,
  WaitReason,
} from "./api";

// What each page level (prompts.RepoEpoch, prompts.ProjectEpoch) brought,
// for the queue's restyle reason; a bump adds its line here.
const S_EPOCH_REPO: Record<number, string> = {
  3: "IT-Begriffe englisch statt wörtlich übersetzt",
};
const S_EPOCH_PROJECT: Record<number, string> = {
  2: "Projektseite nur Verdrahtung, als System oder Sammlung beurteilt",
  3: "IT-Begriffe englisch statt wörtlich übersetzt",
};

export const S = {
  app: "Almanach",
  nav: {
    label: "Seiten",
    projects: "Projekte",
    deps: "Abhängigkeiten",
    sources: "Quellen",
    queue: "Queue",
    costs: "Kosten",
  },
  costs: {
    title: "Kosten",
    failed: "Die Kosten konnten nicht geladen werden.",
    lede: "Was die eigenen Modellaufrufe von Almanach kosten, nach Monat (UTC) und Projekt: die Notizen (ein Aufruf pro gemergtem Pull Request) getrennt von den Runs (ein Repository lesen oder seine Seiten neu fassen) und den PROJECT.md (eine pro Klick in den Quellen). Nur das eigene Hauptbuch; was noergler für seine Reviews zahlt, steht auf den Notizen.",
    empty: "Noch kein Modellaufruf.",
    tiles: {
      today: "Heute ausgegeben (UTC)",
      budget: "Tagesbudget",
      noBudget:
        "Kein Budget gesetzt (BACKEND_DAILY_BUDGET_USD, nur mit Quellen: es hält die Runs)",
      budgetNote: (spent: string, budget: string) =>
        `${spent} von ${budget}; ausgeschöpft, warten die Runs bis zum nächsten Tag (UTC), die Notizen und das Deaktivieren nicht`,
      // What the budget counts beyond the priced sum.
      budgetUnpriced:
        "Aufrufe ohne Preis zählen gegen das Budget wie der teuerste bepreiste Aufruf der letzten 30 Tage.",
    },
    headers: ["Projekt", "Notizen", "Runs", "PROJECT.md", "Aufrufe", "Kosten"],
    monthTotal: "Summe",
    unpriced: (n: number) =>
      n === 1 ? "1 Aufruf ohne Preis" : `${n} Aufrufe ohne Preis`,
    calls: (n: number) => (n === 1 ? "1 Aufruf" : `${n} Aufrufe`),
  },
  search: {
    title: "Suche",
    box: "Wiki durchsuchen",
    placeholder: "Suchen",
    failed: "Die Suche konnte nicht ausgeführt werden.",
    tooShort: "Mindestens zwei Zeichen.",
    tooLong: (n: number) => `Höchstens ${n} Zeichen, die Suche kürzer fassen.`,
    quoted: (q: string) => `«${q}»`,
    none: (q: string) => `Keine Treffer für «${q}».`,
    hits: (n: number, pages: number) =>
      `${n === 1 ? "1 Treffer" : `${n} Treffer`} auf ${pages === 1 ? "einer Seite" : `${pages} Seiten`}`,
    more: (n: number) =>
      n === 1
        ? "Und 1 weitere Seite, die Suche enger fassen."
        : `Und ${n} weitere Seiten, die Suche enger fassen.`,
    project: (key: string) => `Projekt ${key}`,
  },
  deps: {
    title: "Abhängigkeiten",
    failed: "Die Abhängigkeiten konnten nicht geladen werden.",
    lede: "Welche Repositories der Quellen welche anderen verwenden. Almanach liest dafür nach jedem Sync die Build-Manifeste (go.mod, pom.xml, package.json, Gradle: build.gradle(.kts), settings.gradle(.kts), gradle.properties, gradle/libs.versions.toml; Python: pyproject.toml, requirements.txt; Rust: Cargo.toml; im Root-Verzeichnis, eine Ebene darunter und in jedem Ordner, den der Build als Gradle-Projekt, Maven-Modul oder Workspace-Paket einbindet) und verbindet, was ein Repository verlangt, mit dem, was ein anderes liefert. Code zieht die Kanten, kein Modell. Eine Verwendung nur für Tests, für den Build oder als Versionsvorgabe ist ocker gestrichelt; die Karten und die Beschreibungen zählen nur die Verwendungen zur Laufzeit. Jedes Repository wird einmal geclont und beschrieben, wie viele es auch verwenden.",
    disabled:
      "Abhängigkeiten gibt es nur zwischen Quellen, und Quellen sind auf dieser Instanz nicht konfiguriert (BACKEND_REPOS_DIR).",
    empty:
      "Noch keine Abhängigkeit: kein Repository der Quellen verwendet ein anderes, oder die Manifeste sind noch nicht gelesen.",
    graph: "Graph",
    tooLarge: (n: number) =>
      `${n} Repositories sind zu viele für einen Graphen; die Tabelle zeigt alle.`,
    // Under the graph when it holds a dashed edge.
    dashed:
      "Ocker gestrichelt: nur für Tests, den Build oder als Versionsvorgabe verwendet.",
    // Under the graph when all scopes would not fit and the main ones do.
    mainOnly:
      "Der Graph zeigt nur die Verwendungen zur Laufzeit; mit allen wäre er zu gross. Die Tabelle zeigt alle.",
    // What a dashed use is for, on its edge and in the table.
    scopes: {
      main: "",
      managed: "Versionen",
      dev: "Dev",
      test: "Test",
      indirect: "Indirekt",
    } as Record<string, string>,
    shared: "Geteilte Repositories",
    headers: ["Repository", "Verwendet von"],
    ambiguous: "Mehrdeutig",
    ambiguousHint:
      "Diese Koordinaten liefern mehrere Repositories (ein Fork, eine Kopie). Almanach kann nicht wissen, welches gemeint ist, und zieht zu keinem eine Kante.",
    clonedTwice:
      "Dasselbe Repository, von zwei Quellen geclont: eines entfernen, dann zählen die Kanten wieder.",
    uses: "Verwendet",
    usedBy: "Verwendet von",
    also: (names: string) => `auch ${names}`,
    // The repository page's rail: what it uses · what uses it.
    count: (uses: number, usedBy: number) => `${uses} · ${usedBy}`,
  },
  // The parts inside a repository, read from its builds by code.
  parts: {
    title: "Innere Abhängigkeiten",
    hint: "Aus Builds und Code gelesen, kein Modell: Gradle-Projekte, Maven-Module, Workspace-Pakete und Module nebeneinander, dazu die Go- und Python-Packages (auf ihre oberste Ebene gefaltet), und welches welches verwendet. Der Graph zeigt die Module ganz, von den Packages nur die Entry Points und was mindestens zwei davon verwenden; die Tabelle zeigt alle.",
    graph: "Graph",
    tooLarge: (n: number) =>
      `${n} Teile sind zu viele für einen Graphen; die Tabelle zeigt alle.`,
    headers: ["Teil", "Verwendet", "Verwendet von"],
    kinds: { app: "App", lib: "Library", "": "" } as Record<string, string>,
  },
  // Topics and schemas, read from the code by the poller, joined at read.
  msg: {
    title: "Messaging",
    hint: "Aus dem Code gelesen, kein Modell: Topics, die ein Listener, ein Producer, Kafka Streams oder eine Binding-Konfiguration nennt, und die Schemas (Avro, Protobuf). Ein Topic-Name, den der Code nur zur Laufzeit kennt, fehlt.",
    topic: "Topic",
    direction: "Rolle",
    peers: "Gegenstelle",
    producers: "Producer",
    consumers: "Consumer",
    declared: "Genannt von",
    produces: "Producer",
    consumes: "Consumer",
    declares: "Genannt",
    noPeer: "Keine unter den Quellen",
    schemas: "Schemas",
    schema: "Schema",
    kind: "Art",
    file: "Datei",
    // The rail: topics · schemas.
    count: (topics: number, schemas: number) => `${topics} · ${schemas}`,
    tooLarge: (n: number) =>
      `${n} Knoten sind zu viele für ein Bild; die Tabelle zeigt alle.`,
  },
  // Interfaces: HTTP APIs, gRPC and SOAP services, read from the code by
  // the poller.
  ifc: {
    title: "Schnittstellen",
    hint: "Aus dem Code gelesen, kein Modell: OpenAPI- und Swagger-Specs, die Routen im Code (Spring, JAX-RS, NestJS, Express, Go), gRPC-Services aus .proto und SOAP-Services aus WSDLs, die Build oder Code nennen. Ein Pfad, den der Code nur zur Laufzeit kennt, fehlt.",
    projectHint:
      "Was die Repositories des Projekts anbieten, aus dem Code gelesen. Verträge, die ein Repository nur für einen Client hält, stehen auf seiner Seite.",
    codeRoutes: "Routen im Code",
    declares: "Nur genannt",
    declaresHint:
      "Der Vertrag liegt hier, aber kein Code hier bedient ihn: meist die API eines anderen Service, für einen Client.",
    method: "Methode",
    path: "Pfad",
    summary: "Beschreibung",
    base: (path: string) => `Basis ${path}`,
    operations: (n: number) => (n === 1 ? "1 Operation" : `${n} Operationen`),
    kinds: { http: "HTTP", grpc: "gRPC", soap: "SOAP" },
    // The rail: offered · declared.
    count: (offers: number, declares: number) =>
      declares > 0 ? `${offers} · ${declares}` : `${offers}`,
  },
  common: {
    loading: "Lädt …",
    none: "keine",
    never: "nie",
    sep: "·",
  },
  // The ways out of a page: its Markdown export for an agent, a
  // statement's own address for a colleague.
  copy: {
    openMarkdown: "Als Markdown öffnen",
    url: "URL kopieren",
    done: "Kopiert",
    statementAnchor: "#",
    statementLink: (name: string) => `Link zur Aussage ${name}`,
    statementLinkHint: "Adresse dieser Aussage kopieren",
  },
  projects: {
    title: "Projekte",
    lede: "Was Almanach über jedes Projekt weiss: pro Repository und pro Projekt eine Beschreibung, aus dem Code gelesen und mit den Notizen gemergter Pull Requests fortgeschrieben, nie von Hand.",
    failed: "Die Projekte konnten nicht geladen werden.",
    empty:
      "Noch kein Projekt: die erste Notiz kommt mit dem ersten gemergten Pull Request eines Teams mit almanach-Block.",
    team: "Team",
    repos: (n: number) => (n === 1 ? "1 Repository" : `${n} Repositories`),
    notes: (n: number) => (n === 1 ? "1 Notiz" : `${n} Notizen`),
    revision: (n: number) => `Revision ${n}`,
    noDescription: "noch keine Beschreibung",
    firstDescription:
      "Die erste Beschreibung schreibt Almanach aus dem Code, sobald der Clone bereit ist, oder der nächste gemergte Pull Request.",
    lastMerge: "letzter Merge",
    usedBy: (n: number) => `Verwendet von ${n}`,
    problems: (n: number) =>
      n === 0
        ? "keine Problemzonen"
        : n === 1
          ? "1 Problemzone"
          : `${n} Problemzonen`,
  },
  project: {
    failed: "Das Projekt konnte nicht geladen werden.",
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
    rootArea: "(Root)",
    recentNotes: "Letzte Notizen",
    noNotes: "Noch keine Notiz.",
    map: "Systemübersicht",
    noMap:
      "Noch keine Systemübersicht: sie entsteht beim nächsten Run, der dieses Projekt aus dem Code beschreibt.",
    // What Almanach judged the project to be, in the page's meta line.
    shape: {
      system: "System",
      collection: "Sammlung",
    },
    shapeHint: {
      system:
        "Almanach liest die Repositories als ein System: die Projektseite beschreibt, wie sie zusammenspielen, die Repository-Seiten, wie jedes funktioniert.",
      collection:
        "Almanach liest die Repositories als Sammlung, die je für sich stehen: die Projektseite hält nur eine kurze Zusammenfassung, alles Weitere steht auf den Repository-Seiten.",
    },
  },
  diagram: {
    titles: {
      flow: "Ablaufdiagramm",
      sequence: "Sequenzdiagramm",
      er: "Datenmodell",
      map: "Systemübersicht",
      repoMap: "Aufbau",
    },
    // The project page's map, compact: its bands without arrows.
    compactHint:
      "Zeigen, fokussieren oder antippen: die Verbindungen einer Box leuchten auf, mit Richtung und Art (gestrichelt ⇢: asynchron oder optional). Die Pfeile zeigt die Vollansicht.",
    // An arrow seen from the box pointed at: out, in, both ways; dashed in
    // the drawing, a boundary (async or optional), dashed here too.
    way: { out: "→", in: "←", both: "↔" },
    dashedWay: { out: "⇢", in: "⇠", both: "⇠⇢" },
    // Said to a screen reader when a box lights its connections, and shown
    // in the line under the map, each way whole.
    connections: (name: string, to: string[]) =>
      to.length === 0
        ? `${name}: keine Verbindungen`
        : `${name}: verbunden mit ${to.join(", ")}`,
    // The map's zones by id: the backend allows these five and no other.
    zones: {
      clients: "Clients",
      entry: "Eingang",
      repositories: "Repositories",
      stores: "Datenspeicher",
      external: "Externe Systeme",
      // Drawn by code from the manifests, never by the model: repositories
      // of other projects this one depends on, whatever they are.
      shared: "Geteilte Repositories",
    } as Record<string, string>,
    // The edge code draws from a repository to one it depends on.
    sharedEdge: "Abhängigkeit",
    // The dependency graph's zones by id, drawn by code.
    depZones: {
      users: "Nutzer",
      shared: "Verwendet",
    } as Record<string, string>,
    // The parts inside a repository by id, drawn by code: those its
    // build calls applications, then the rest.
    partZones: {
      apps: "Anwendungen",
      parts: "Module",
    } as Record<string, string>,
    // A repository's messages, left to right.
    msgZones: {
      from: "Producer",
      in: "Consumed",
      self: "Repository",
      out: "Produced",
      to: "Consumer",
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
    hits: (n: number, of: number, chapters: number) =>
      `${n} von ${of} ${of === 1 ? "Eintrag" : "Einträgen"} ${S.common.sep} ${chapters} Kapitel`,
    noHits: "keine Treffer",
    summary: "Überblick",
    // The repository page's head: its project, and a switch to the
    // project's other repositories.
    project: (key: string) => `Projekt ${key}`,
    position: (at: number, of: number) => `${at} / ${of}`,
    // The narrow chip: the project before the slug.
    chip: (key: string) => `${key} / `,
    up: "↑",
    prevStep: "‹",
    nextStep: "›",
    opens: "▾",
    prev: (slug: string) => `Vorheriges Repository: ${slug}`,
    next: (slug: string) => `Nächstes Repository: ${slug}`,
    switchRepo: "Repository wechseln",
    findRepo: "Repository suchen",
    noRepo: "Kein Repository",
  },
  repo: {
    failed: "Das Repository konnte nicht geladen werden.",
    project: "Projekt",
    single: "Projekt aus einem Repository",
    map: "Aufbau",
    mapDeclined:
      "Kein Aufbau: der letzte Run hat jeden Entwurf verworfen, weil er gegen die Regeln der Karte verstiess. Er wird neu gezeichnet, sobald sich die Beschreibung ändert.",
    notes: "Notizen",
  },
  // A page below today's rules (Regelversion, the queue's word too; user,
  // 2026-11-10): what happens next and what a reader can do (a clear call
  // to action; on its own, say so and roughly when).
  stale: {
    title: "Veraltete Regelversion",
    rule: (scope: "repo" | "project", at: number) => {
      const what = (scope === "project" ? S_EPOCH_PROJECT : S_EPOCH_REPO)[at];
      return (
        `Diese Seite wurde noch nicht nach Regelversion ${at} geprüft` +
        (what ? ` (${what})` : "") +
        ". Einzelne Aussagen können noch der alten Fassung folgen."
      );
    },
    running: "Wird gerade neu gegliedert.",
    queued: "Steht in der Queue und wird bald neu gegliedert. Nichts zu tun.",
    waiting: (eta: string, rank: number) =>
      `Wird automatisch neu gegliedert, voraussichtlich ${eta} (Platz ${rank}). Nichts zu tun.`,
    waitingNoEta: (rank: number) =>
      `Wird automatisch neu gegliedert (Platz ${rank}). Nichts zu tun.`,
    paused: "Die Queue ist pausiert, darum geschieht nichts.",
    budget: "Das Tagesbudget ist aufgebraucht, es geht morgen weiter.",
    manual: {
      off: "Wird nicht automatisch nachgeführt: die automatischen Runs sind abgeschaltet.",
      failed:
        "Wird nicht automatisch nachgeführt: die automatischen Versuche sind gescheitert.",
    } as Record<string, string>,
    toQueue: "Queue ansehen",
    resume: "Queue fortsetzen",
    toSource: "In den Quellen neu gliedern",
    // About when, rounded: an estimate, never a promise.
    eta: (seconds: number) => {
      const min = Math.max(1, Math.round(seconds / 60));
      if (min < 60) {
        return min === 1 ? "in etwa einer Minute" : `in etwa ${min} Minuten`;
      }
      const h = Math.round(min / 60);
      if (h < 48) {
        return h === 1 ? "in etwa einer Stunde" : `in etwa ${h} Stunden`;
      }
      return `in etwa ${Math.round(h / 24)} Tagen`;
    },
  },
  // One revision's page: the description as it stood, and what it changed
  // against the revision before.
  revision: {
    title: (n: number) => `Revision ${n}`,
    of: (n: number, current: number) =>
      n === current
        ? `Revision ${n}, die aktuelle`
        : `Revision ${n} von ${current}`,
    failed: "Die Revision konnte nicht geladen werden.",
    changes: "Änderungen",
    against: (previous: number) => `Gegenüber Revision ${previous}`,
    first: "Die erste Revision: alles neu",
    added: "Neu",
    changed: "Geändert",
    dropped: "Entfernt",
    // A baseline run confirmed the statement against the code, nothing
    // else of it changed.
    rechecked: "Bestätigt",
    summaryChanged: "Überblick geändert",
    mapChanged: "Neu gezeichnet",
    shape: "Einordnung",
    shapeChanged: "Neu beurteilt: System oder Sammlung",
    nothing: "Keine Aussage hinzugekommen, geändert oder entfernt.",
    description: "Beschreibung in dieser Revision",
    previous: "‹ Vorherige",
    next: "Nächste ›",
    current: "Aktuelle Seite",
    // A page's history, the list on the repository and the project page.
    list: "Revisionen",
    none: "Noch keine Revision.",
    from: "aus",
    marked: "aktuell",
    statements: (n: number) => (n === 1 ? "1 Aussage" : `${n} Aussagen`),
    chars: (n: number) => `${fmtInt(n)} Zeichen`,
  },
  kinds: {
    component: "Komponenten",
    interface: "Schnittstellen",
    flow: "Abläufe",
    rule: "Regeln",
    invariant: "Invarianten",
    entity: "Entitäten",
    decision: "Entscheidungen",
    extension: "Erweiterungen",
    pitfall: "Fallstricke",
  },
  // One statement's kind, the label over its name in an area's chapter.
  statementKind: {
    component: "Komponente",
    interface: "Schnittstelle",
    flow: "Ablauf",
    rule: "Regel",
    invariant: "Invariante",
    entity: "Entität",
    decision: "Entscheidung",
    extension: "Erweiterung",
    pitfall: "Fallstrick",
  } satisfies Record<Kind, string> as Partial<Record<string, string>>,
  // The project page's labels where they differ: its entities are the ones
  // that cross repositories, who owns them and how their ids travel (the
  // export's heading their plural, held to it by a test).
  projectStatementKind: {
    entity: "Geteilte Entität",
  } satisfies Partial<Record<Kind, string>> as Partial<Record<string, string>>,
  // otherArea is the chapter of statements in no area, beside named areas
  // where its first statement stands (the Markdown export's word for them
  // under a kind, held to it by a test); all the one chapter of a page not
  // laid out yet; none a page without statements.
  areas: {
    otherArea: "Weiteres",
    all: "Aussagen",
    none: "Noch keine Aussagen.",
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
    failed: "Die Notiz konnte nicht geladen werden.",
    ticket: "Ticket",
    merge: "Merge",
    summary: "Zusammenfassung",
    noSummary:
      "Die Notiz hat keine Zusammenfassung: der PR hat nichts über das System gelehrt.",
    taught: "Eingearbeitet in",
    taughtHint: "die aktuellen Aussagen, die diesen Pull Request nennen",
    taughtPending: "Die Notiz ist noch nicht eingearbeitet.",
    taughtNone:
      "Keine aktuelle Aussage nennt diesen Pull Request: die Notiz hat nichts gelehrt, oder was sie lehrte, wurde seither ersetzt.",
    taughtProject: (key: string) => `Projekt ${key}`,
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
    // consolidated_at: also set on a note refused or ignored without a call.
    consolidated: "Abgeschlossen",
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
    lede: "Die Projekte, deren Code Almanach selbst clont: ein Bitbucket-Projekt, dessen Repositories Almanach über die API liest, oder ein Projekt, dessen Repositories von Hand eingetragen werden. Die Clones sind die Grundlage, die später beschrieben und mit jedem gemergten Pull Request fortgeschrieben wird.",
    failed: "Die Quellen konnten nicht geladen werden.",
    failedOne: "Die Quelle konnte nicht geladen werden.",
    // A write that got no answer at all (the server's own refusals carry
    // their message).
    unreachable: "Der Server war nicht erreichbar. Bitte erneut versuchen.",
    empty: "Noch keine Quelle.",
    disabled:
      "Quellen sind auf dieser Instanz nicht konfiguriert (BACKEND_REPOS_DIR).",
    headers: ["Slug", "Art", "Host", "Token", "Repos", "Status", "Letzter Run"],
    kind: { bitbucket: "Bitbucket", static: "Einzeln" } as Record<
      string,
      string
    >,
    kindLabel: {
      bitbucket: "Ganzes Projekt (Bitbucket)",
      // No-break spaces: a narrow form wraps before the bracket, never inside it.
      static: "Einzelnes Repository (z.\u00a0B.\u00a0GitHub,\u00a0Bitbucket)",
    } as Record<string, string>,
    kindHint: {
      bitbucket:
        "Almanach liest die Repositories des Projekts aus der Bitbucket-API und listet sie bei jedem Run neu: neue kommen dazu, entfernte bleiben als verschwunden stehen, bis du sie entfernst.",
      static: "Ein Repository per Clone-URL, von jedem Git-Host über https.",
    } as Record<string, string>,
    tokenStored: "Hinterlegt",
    tokenNone: "Öffentlich",
    tokenAs: (auth: string, user: string) =>
      auth === "basic" ? `Basic als ${user || "x-access-token"}` : "Bearer",
    listingFailed: "Listing fehlgeschlagen",
    listedAt: "gelistet",
    neverListed: "noch nie gelistet",
    // Beside the Einzeln chip, where a Bitbucket source links its project.
    manual: "Per Clone-URL",
    project: "Projekt",
    counts: {
      ready: (n: number) => `${n} bereit`,
      active: (n: number) => `${n} läuft`,
      failed: (n: number) => `${n} fehlgeschlagen`,
      missing: (n: number) => `${n} verschwunden`,
      inactive: (n: number) => `${n} deaktiviert`,
      none: "Keine Repositories",
    },
    newSource: "Neue Quelle",
    form: {
      kind: "Art",
      slug: "Slug",
      // A Bitbucket project comes by its page's URL: host and key are read
      // off it, the key shown read-only. The key is Bitbucket's, not a name
      // Almanach gives: no rename.
      projectURL: "Projekt-URL",
      projectURLPlaceholder: "https://bitbucket.example.invalid/projects/SHOP",
      projectURLHint:
        "Die Seite des Projekts in Bitbucket, aus der Adresszeile kopiert: https://<host>/projects/<Projektschlüssel>. Eine Seite darunter, etwa die eines Repositories, geht auch.",
      projectURLInvalid:
        "Keine Projekt-URL von Bitbucket Server: erwartet wird https://<host>/projects/<Projektschlüssel>.",
      projectKey: "Projektschlüssel",
      projectKeyHint:
        "Aus der URL, in Grossbuchstaben wie in Bitbucket. Unter ihm listet Almanach die Repositories, und an ihm finden die Notizen von noergler das Projekt; er lässt sich nicht ändern.",
      // A hand-filled project's first repository, created with it.
      firstRepo: "Clone-URL des ersten Repositories",
      firstRepoHint:
        "Weitere Repositories kommen auf der Seite der Quelle dazu. Ein Projekt aus einem Repository hat keine eigene Projektseite: die Seite des Repositories ist die des Projekts.",
      slugFromURL:
        "Füllt sich beim Einfügen der URL mit dem Namen des Repositories; von Hand geändert, bleibt er. Leer: der Name aus der URL.",
      branch: "Branch",
      branchPlaceholder: "Default-Branch des Remotes",
      host: "Bitbucket-Host",
      hostHint:
        "Aus der URL. Die Clone-URLs der gelisteten Repositories müssen auf diesen Host zeigen: über einen anderen Namen erreicht, scheitert jeder Sync.",
      token: "Personal Access Token",
      optional: "(optional)",
      tokenPlaceholder: "Leer lassen für öffentliche Repositories",
      tokenHint: {
        bitbucket:
          "Nur nötig für private Repositories, ein privates Bitbucket-Projekt oder einen Host, der die API nicht anonym beantwortet. Das Token listet das Projekt über die API und clont seine Repositories.",
        static:
          "Nur nötig für private Repositories. Ein Token für das ganze Projekt: es gilt für jedes Repository darin. Für GitHub ein klassisches Token mit Scope «repo» oder ein Fine-grained-Token mit «Contents: read»; für Bitbucket ein HTTP-Access-Token mit Leserecht, dafür Bearer wählen.",
      } as Record<string, string>,
      tokenAs: "Token senden als",
      bearerHint:
        "Bearer: das Token als Authorization-Header, kein Benutzername nötig. Für Bitbucket Data Center.",
      basicHint:
        "Basic: Benutzername und Token als Basic-Auth-Header. Für GitHub.",
      user: "Benutzername",
      userHint:
        "GitHub ignoriert ihn; Bitbucket prüft ihn (Projekt- und Repo-Token: x-token-auth, persönliche Token: der eigene Name).",
      create: "Anlegen und syncen",
      // Every repository of a new source gets a full run: said before
      // the click, with what one may cost.
      costHint: (calls: number) =>
        `Jedes Repository wird nach dem ersten Clone vollständig beschrieben: bis zu ${calls} Modellaufrufe pro Repository, bei einem Bitbucket-Projekt für jedes gelistete Repository.`,
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
        static: "einzeln eingetragen",
      } as Record<string, string>,
      ready: "Bereit",
      lastSync: "Letzter Sync",
      running: "läuft gerade",
      disk: "Auf der Disk",
    },
    actions: {
      sync: "Sync",
      syncRunning: "Sync läuft gerade.",
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
      hint: "Auch nur die Gross- und Kleinschreibung. Seiten, Notizen und Clones bleiben, der alte Slug führt weiter hierher. Ein Slug, den ein anderes Projekt je hatte, ist vergeben, in jeder Schreibweise.",
      save: "Umbenennen",
    },
    renameRepoForm: {
      title: (slug: string) => `${slug} umbenennen`,
      slug: "Neuer Slug",
      slugHint:
        "Auch nur die Gross- und Kleinschreibung. Beschreibung, Notizen und Runs bleiben, der alte Slug führt weiter hierher. Ein Slug, den ein anderes Repository je hatte, ist vergeben.",
      url: "Neue Clone-URL",
      urlPlaceholder: "(bleibt)",
      urlHint: "Nur https. Eine neue URL holt den Clone neu.",
      save: "Umbenennen",
    },
    confirmRemove: (key: string, repos: number, dir: string) =>
      `Quelle ${key} entfernen? Löscht die Quelle, ihre ${repos} Repository-Einträge und den Ordner ${dir}. Beschreibungen aus Pull-Request-Notizen bleiben; sie hängen am Projekt, nicht an der Quelle.`,
    confirmRemoveRepo: (slug: string, dir: string) =>
      `Repository ${slug} entfernen? Löscht den Eintrag und den Clone unter ${dir}. Der Eintrag kann jederzeit neu angelegt werden; der Clone wird dann neu geholt.`,
    yesRemove: "Ja, entfernen",
    // Instant: clone gone, notes ignored, page out of the wiki. The project
    // page is the queue's: the departure run (one model call).
    confirmDeactivateRepo: (slug: string) =>
      `Repository ${slug} deaktivieren? Der Clone wird gelöscht, neue Notizen werden ignoriert und die Seite verschwindet aus dem Wiki; das Listing nimmt es nicht wieder auf. Ein Run in der Queue nimmt es aus der Projektseite und der Systemkarte (ein Modellaufruf). Die Seite selbst bleibt erhalten und ist mit «Aktivieren» wieder da.`,
    yesDeactivate: "Ja, deaktivieren",
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
      "Status",
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
      "Bei einer Bitbucket-Quelle werden Repositories nicht von Hand hinzugefügt: die Liste ist die der API. Ein Repository, das die API nicht mehr liefert, wird als «verschwunden» markiert und sein Clone bleibt, bis du ihn entfernst. Ein Repository, das nicht dazugehören soll, deaktivierst du: es bleibt draussen, auch wenn die API es weiter liefert.",
    state: {
      pending: "Ausstehend",
      syncing: "Läuft",
      ready: "Bereit",
      failed: "Fehlgeschlagen",
      missing: "Verschwunden",
    } as Record<string, string>,
    deactivated: "Deaktiviert",
    repoActions: {
      sync: "Sync",
      retry: "Erneut",
      remove: "Entfernen",
      removeClone: "Clone entfernen",
      rename: "Umbenennen",
      deactivate: "Deaktivieren",
      activate: "Aktivieren",
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
      add: "Hinzufügen und clonen",
    },
    listingError: "Letztes Listing fehlgeschlagen:",
    listingErrorHint:
      "Die Repository-Liste ist die des letzten erfolgreichen Listings; bekannte Repositories werden weiter gesynct.",
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
      // A restyle, open: the pages brought to new rules, no code read.
      restyle: "Wird neu gegliedert",
      done: "Beschrieben",
      partial: "Teilweise",
      failed: "Fehlgeschlagen",
    },
    at: (sha: string) => `Bei ${sha}`,
    soFar: "bisher",
    // The share of the readable code a done run read, a percent: twelve
    // subjects of a large repository once read a tenth and the page said
    // nothing of it. A few bytes read are under one, none read 0.
    readShare: (pct: number) =>
      pct > 0 && pct < 1
        ? "unter 1 % des Codes gelesen"
        : `${Math.round(pct)} % des Codes gelesen`,
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
      calls: "Aufrufe des Runs aufgebraucht",
      bytes: "Lesebudget des Runs überschritten",
      tokens: "Token-Budget des Runs überschritten",
      failed: "Fehler: Modellaufruf fehlgeschlagen",
      refused: "Fehler: Antwort regelwidrig, verworfen",
      no_page: "Projekt ohne Seite",
      "": "Grund nicht erfasst",
    } satisfies Record<SkipReason, string>,
    // Under the skipped parts: when they are read again, on its own (the
    // poller, auto_retry: a day after the run) or with the next run,
    // sooner by the button.
    retryAuto: "Wird von selbst erneut gelesen.",
    retryManual: "Der nächste Run liest sie erneut, Aktualisieren sofort.",
    failedRun: "Beschreibung fehlgeschlagen:",
    upToDate:
      "Der Head ist schon beschrieben: Neu beschreiben liest alles erneut.",
    described: (n: number) => `${n} beschrieben`,
    actions: {
      describe: "Beschreiben",
      reconcile: "Aktualisieren",
      full: "Neu beschreiben",
      restyle: "Neu gliedern",
    },
    restyleHint:
      "Bringt die Seiten auf die aktuelle Regelversion, ohne Code zu lesen. Eine Seite, die schon aktuell ist, bleibt unverändert.",
    // A full run is paid for whole: confirmed, with what the last one cost.
    confirmFull: (slug: string, calls: number, last: string) =>
      `${slug} neu beschreiben? Liest das ganze Repository erneut, bis zu ${calls} Modellaufrufe` +
      (last === "" ? "." : `; der letzte Run kostete ${last}.`),
    yesFull: "Ja, neu beschreiben",
  },
  // A repository's .agents/PROJECT.md: written from its page, pushed to a
  // branch of its own, the text to copy when the push is refused.
  brief: {
    action: "PROJECT.md erzeugen",
    noPage: "Erst beschreiben: die PROJECT.md entsteht aus der Seite.",
    state: {
      requested: "PROJECT.md wird erzeugt",
      failed: "PROJECT.md fehlgeschlagen",
      pushing: "PROJECT.md wird gepusht",
      pushed: "PROJECT.md gepusht",
      unchanged: "PROJECT.md unverändert",
      push_failed: "Push abgelehnt",
    } satisfies Record<BriefState, string>,
    pushedTo: (branch: string, sha: string) => `Nach ${branch} (${sha})`,
    unchangedNote: "Der Standard-Branch hat sie schon.",
    waitBudget: "Tagesbudget aufgebraucht, wartet auf den nächsten Tag.",
    show: "Text anzeigen",
    title: "PROJECT.md",
    pushRefused: (branch: string, why: string) =>
      `Der Push nach ${branch} wurde abgelehnt (${why}). Den Text in die Datei unten kopieren.`,
    fromRevision: (path: string, revision: number) =>
      `Für ${path}, aus Revision ${revision} der Seite.`,
    copy: "Kopieren",
    copied: "Kopiert",
    copyFailed: "Kopieren nicht möglich, bitte von Hand markieren.",
    failedLoad: "Der Text konnte nicht geladen werden.",
  },
  queue: {
    title: "Queue",
    failed: "Die Queue konnte nicht geladen werden.",
    polling: (s: number) => `Aktualisiert alle ${s} s, solange etwas läuft`,
    more: (n: number) => (n === 1 ? "Und 1 weitere" : `Und ${n} weitere`),
    // The pause: the worker takes nothing new, the item in flight finishes.
    pausedBadge: "Pausiert",
    paused: (since: string) => `Pausiert seit ${since}`,
    pausedHint:
      "Almanach nimmt nichts Neues: keine Notiz, keinen Run-Schritt, kein Neugliedern, keine PROJECT.md. Was gerade läuft, wird fertig. Webhook und Sync laufen weiter; die Arbeit wartet.",
    pause: "Pausieren",
    pauseHint:
      "Nimmt nach dem laufenden Aufruf nichts Neues mehr, bis fortgesetzt wird; bleibt über einen Neustart.",
    resume: "Fortsetzen",
    pauseFailed: "Die Queue konnte nicht pausiert oder fortgesetzt werden.",
    // Why a run was queued, under its meta line.
    why: "Warum:",
    reason: {
      first: "Noch nie beschrieben, erstes Lesen.",
      retry: (n: number, of: number, err: string) =>
        `Neuer Versuch ${n} von ${of}` +
        (err === "" ? "." : `; zuletzt: ${err}`),
      unfinished:
        "Der letzte Run hat Teile übersprungen oder keine Karte gezeichnet: das wird nachgeholt.",
      interval: (days: number, from: string, to: string) =>
        `Abgleich nach ${days} Tagen, ${from} bis ${to}.`,
      intervalFull: (days: number) =>
        `Nie fertig beschrieben; nach ${days} Tagen neu gelesen.`,
      repoLevel: (n: number) =>
        `Repository-Seite auf Regelversion ${n}` +
        (S_EPOCH_REPO[n] ? ` (${S_EPOCH_REPO[n]})` : ""),
      projectLevel: (n: number) =>
        `Projektseite auf Regelversion ${n}` +
        (S_EPOCH_PROJECT[n] ? ` (${S_EPOCH_PROJECT[n]})` : ""),
      // Between a restyle's two page levels.
      levelSep: "; ",
      manual: "Von Hand ausgelöst.",
      departure: "Repository deaktiviert: die Projektseite nimmt es heraus.",
      returned: (from: string, to: string) =>
        `Wieder aktiviert; Abgleich von ${from} bis ${to}.`,
      returnedFull: "Wieder aktiviert, nie fertig beschrieben: neu gelesen.",
    },
    tiles: {
      now: "Jetzt",
      waiting: "Wartet",
      spent: "24 h ausgegeben",
      sync: "Sync",
    },
    idle: "–",
    nothingToDo: "Nichts zu tun",
    counts: (notes: number, runs: number) =>
      [
        notes > 0 ? (notes === 1 ? "1 Notiz" : `${notes} Notizen`) : "",
        runs > 0 ? (runs === 1 ? "1 Run" : `${runs} Runs`) : "",
      ]
        .filter((s) => s !== "")
        .join(", "),
    calls: (n: number) => (n === 1 ? "1 Aufruf" : `${n} Aufrufe`),
    // Beside the calls: those without a price, not in the sum.
    unpriced: (n: number) => `${n} ohne Preis`,
    unpricedHint:
      "Ohne Preis: ein Aufruf, für den das Gateway keine Kosten meldete, meist eine abgebrochene Antwort. Nicht in der Summe; die Kosten sind unbekannt, nicht null. Gegen das Tagesbudget zählt jeder des laufenden Tages (UTC) wie der teuerste bepreiste Aufruf der letzten 30 Tage; gibt es keinen, zählt er nichts.",
    sync: {
      running: "Läuft",
      ready: "Bereit",
      progress: (repo: string, done: number, total: number) =>
        `${repo}, ${done + 1} von ${total}`,
      next: (at: string) => `Nächster Zyklus ${at}`,
      manual: "Nur manuell",
    },
    lane: {
      now: "Jetzt",
      next: "Als Nächstes",
      after: "Danach",
    },
    note: "Notiz",
    // A repository's brief in flight: its call or its push.
    brief: "PROJECT.md",
    briefRunning: (since: string) => `erzeugt oder pusht seit ${since}`,
    run: (id: number) => `Run #${id}`,
    mode: {
      full: "Vollständig",
      reconcile: "Abgleich",
      restyle: "Neu gegliedert",
    } satisfies Record<QueueRun["mode"], string>,
    trigger: {
      manual: "manuell",
      first_ready: "Erster Clone",
      monthly: "Monatlich",
      retry: "Erneut",
      departure: "Deaktiviert",
      epoch: "Neue Regeln",
    } satisfies Record<QueueRun["trigger"], string>,
    step: {
      catalogue: "Katalog",
      overview: "Überblick",
      project: "Projektseite",
      repo_map: "Aufbau",
      restyle: "Repository-Seite",
      project_restyle: "Projektseite",
    } satisfies Record<Exclude<StepKind, "subject">, string>,
    stepSub: {
      catalogue: "Teile und ihre Dateien",
      overview: "README und Dokumente",
      project: "aus den Repository-Seiten",
      repo_map: "Repository-Karte",
      restyle: "neu gegliedert, ohne Code",
      project_restyle: "neu gegliedert, ohne Code",
    } satisfies Record<Exclude<StepKind, "subject">, string>,
    files: (n: number, size: string) =>
      n === 1 ? `1 Datei, ${size}` : `${n} Dateien, ${size}`,
    callRunning: (since: string) => `läuft seit ${since}`,
    callsFailed: (n: number) =>
      n === 1 ? "1 Aufruf ohne Antwort" : `${n} Aufrufe ohne Antwort`,
    skipped: (why: string) => `übersprungen: ${why}`,
    stepOf: (i: number, n: number) => `Schritt ${i} von ${n}`,
    continues: (n: number) =>
      n === 1 ? "weiter, 1 Schritt" : `weiter, ${n} Schritte`,
    received: (at: string) => `eingegangen ${at}`,
    queuedSince: (at: string) => `wartet seit ${at}`,
    started: (at: string, ago: string) => `gestartet ${at}, vor ${ago}`,
    soFar: "bisher",
    // A duration past ten minutes; under, it reads 0:42.
    minutes: (m: number) => `${m} min`,
    hours: (h: number, m: number) => `${h} h ${m} min`,
    attempt: (n: number, max: number) => `Versuch ${n + 1} von ${max}`,
    heldBy: (id: number) => `Wartet auf Run #${id}.`,
    // A repository's notes are consolidated together once a day: when this
    // one's turn comes, and how many notes one call takes.
    dueAt: (at: string) => `Vorgemerkt, frühestens ${at}`,
    batch: (n: number) => `${n} Notizen in einem Aufruf`,
    // A consolidated note's cost is its call's, shared with the others.
    shared: (n: number) => `Für ${n} Notizen`,
    commit: (from: string, to: string) =>
      from === "" ? `Commit ${to}` : `von ${from} auf ${to}`,
    wait: {
      timeout: "Modell antwortet nicht (Timeout)",
      unavailable: "Modell antwortet nicht",
      rate_limit: "Modell ausgelastet",
      key_refused: "API-Key abgelehnt",
      busy: "Datenbank belegt",
      write: "Datenbank nimmt keine Schreibzugriffe an (Disk voll?)",
      clone_busy: "Clone belegt",
      placing: "Git ordnet einen Merge-Commit ein",
      budget:
        "Tagesbudget ausgeschöpft, Runs warten bis morgen (UTC), ausser beim Deaktivieren",
    } satisfies Record<WaitReason, string>,
    until: (at: string, left: string) => `${at}, in ${left}`,
    soon: (s: number) => `in ≤ ${s} s`,
    waitingSince: (since: string) => `seit ${since}`,
    empty: "Nichts wartet.",
    emptyNext: (at: string) => `Nichts wartet. Nächster Sync ${at}.`,
    recent: "Zuletzt, 24 h",
    recentHeaders: ["Zeit", "Was", "Ergebnis", "Kosten"],
    recentEmpty: "In den letzten 24 h nichts abgeschlossen.",
    runDone: "Beschrieben",
    runRestyled: "Neu gegliedert",
    runFailed: "Fehlgeschlagen:",
    retryAt: (at: string) => `Erneut automatisch frühestens ${at}`,
    noteOutcome: {
      ok: "Eingearbeitet",
      timed_out: "Nicht eingearbeitet: Timeout",
      unparseable: "Nicht eingearbeitet: Antwort unlesbar",
      too_long: "Nicht eingearbeitet: Antwort zu lang",
      error: "Nicht eingearbeitet: Aufruf fehlgeschlagen",
      "": "Ohne Modellaufruf abgeschlossen",
    } as Record<string, string>,
    failedNotes: "Nicht eingearbeitet",
    failedHint:
      "Der Modellaufruf ist an der Notiz gescheitert. Nach dem nächsten Run des Repositories, spätestens nach einem Tag, wird sie erneut eingearbeitet.",
    givenUpHint:
      "Eine aufgegebene Notiz wird erst mit einer neuen Lieferung wieder eingearbeitet.",
    failedHeaders: ["Zeit", "Notiz", "Grund", "Versuche"],
    failedAttempts: (n: number, max: number) => `${n} von ${max}`,
    givenUp: "Aufgegeben",
  },
  auth: {
    checking: "Session wird geprüft …",
    redirecting: "Weiterleitung zum Login …",
    signedOut:
      "Ausgeloggt. Die Session beim Identity Provider läuft weiter; wer sie beenden will, loggt sich auch dort aus.",
    failed:
      "Der Login konnte nicht abgeschlossen werden. Bitte erneut versuchen; schlägt er wieder fehl, steht der Grund im Server-Log.",
    unchecked: (status: number) =>
      `Die Session konnte nicht geprüft werden (HTTP ${status}).`,
    unreadable:
      "Die Session konnte nicht geprüft werden: die Antwort des Servers war unlesbar.",
    offline:
      "Die Session konnte nicht geprüft werden: der Server ist nicht erreichbar.",
    misconfigured:
      "Der Login ist falsch eingerichtet: der Identity Provider passt nicht zur Konfiguration. Der Grund steht im Server-Log; nach der Korrektur und einem Neustart erneut versuchen.",
    signOutFailed: "Logout fehlgeschlagen, bitte erneut versuchen.",
    unavailable:
      "Der Login ist im Moment nicht möglich: der Identity Provider antwortet nicht. Almanach versucht es laufend weiter; bitte später erneut versuchen.",
    loop: "Der Login hat gerade eben keine Session ergeben. Bitte erneut einloggen; geschieht das wieder, steht der Grund im Server-Log.",
    signIn: "Login",
    signOut: "Logout",
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
