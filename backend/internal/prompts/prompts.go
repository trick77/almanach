// Package prompts holds the model prompts, compiled into the binary: there
// is no file to mount over, and none to miss at startup.
package prompts

import _ "embed"

// Merge folds one note into the repository and project descriptions.
//
//go:embed almanach_merge.txt
var Merge string

// Catalogue names the subjects a repository is made of, for a baseline run.
//
//go:embed almanach_catalogue.txt
var Catalogue string

// Describe asks what one part of a repository's code teaches its page, as a
// patch: the overview step and every subject step.
//
//go:embed almanach_describe.txt
var Describe string

// Project folds a freshly written repository description into the project
// description, the last step of a baseline run.
//
//go:embed almanach_project.txt
var Project string

// RepoMap draws a repository's map from its description, the last step of
// a baseline run, after the project.
//
//go:embed almanach_repo_map.txt
var RepoMap string

// Diagrams is the diagram rules the merge, describe and project prompts
// share, rendered into their {diagram_rules}.
//
//go:embed almanach_diagrams.txt
var Diagrams string

// DiagramsOff stands in for Diagrams with BACKEND_DIAGRAM_MAX_PER_PAGE=0, when no
// statement schema offers a diagram.
//
//go:embed almanach_diagrams_off.txt
var DiagramsOff string

// Spelling is the spelling rule every prompt that asks for German states,
// word for word: one rule, never five wordings drifting apart.
const Spelling = `"ss", never "ß"; umlauts stay ä, ö, ü, Ä, Ö, Ü, never spelled out as ae, oe, ue`

// Redacted is what every prompt that reads code or notes says about the
// marker the redact package leaves, word for word. The code removes the
// value; the sentence only keeps the model from inventing one.
const Redacted = "`<redacted>` in what you read marks a credential the code removed before you saw it: never guess, reconstruct or quote its value; where it matters, say that a credential is configured there."

// Layout is the text layout rule every prompt that writes statement text
// or a summary states, word for word: structure where it helps a human
// reader, prose by default, carried by the schema's blocks (Block), never
// by newlines inside a JSON string, which a model drops (2026-11-01).
const Layout = "Layout, for a human reader as much as for an agent: a text (a statement's `text`, the `summary`) is an array of blocks in reading order, and the code joins them. Never a newline, `<br>`, `#### ` or a list marker inside a block's `text` or an item: a new paragraph, sub-heading or item is a block or an item of its own. `paragraph`: full sentences in `text`, the default. `list`, or `numbered` where the order matters: only for a true enumeration (steps, distinct options, conditions, fields), one entry per item in `items`, no marker written, never nested, `text` a lead-in sentence or empty. `heading`: a short sub-heading in `text`, only in a long statement with distinct parts, before each part. Inside a block: `**bold**` sparingly, for the one term a reader must not miss; identifiers, paths and commands in backticks. Nothing else: no tables, links, quotes, code blocks or HTML. A short statement stays plain paragraphs."

// SummaryLayout is how a summary takes Layout, word for word in every
// prompt that writes one: the summary is the page's longest text and the
// first a reader sees, never one block of prose. It opens with prose (a
// card shows its first sentence), and a heading names what its part says:
// titled after the order's parts, every page opened with "Zweck und
// Abgrenzung" over text that drew no boundary (2026-11-01).
const SummaryLayout = "The summary takes the same blocks. It opens with a paragraph, never a heading: what this is. Each later part of its order that has something to say gets a `heading` naming what that part says here (\"Drei Dienste, eine Datenbank\", \"Vom Upload zum PDF\"), never the order's name for it (never \"Zweck und Abgrenzung\", \"Struktur\", \"Abläufe\", \"Arbeiten daran\"), and paragraphs or a list under it; a part with nothing to say is left out, never a heading over filler. A true enumeration (the repositories and their roles, a repository's main parts) is a `list`, each item its name first in backticks; `**bold**` for the one term of a part a reader must not miss. A long summary is never one paragraph."

// Block is a laid-out text's block as every prompt's output format shows
// it, word for word: the schema's (inference blocks).
const Block = "`<block>` is `{\"kind\": \"paragraph\" | \"heading\", \"text\": \"<text>\"}` or `{\"kind\": \"list\" | \"numbered\", \"text\": \"<lead-in sentence or empty>\", \"items\": [\"<item>\", ...]}`"
