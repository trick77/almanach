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
// reader, prose by default, and only the subset markup.Normalize keeps.
const Layout = "Layout, for a human reader as much as for an agent: by default paragraphs of full sentences, separated by a blank line. A list only where the content is a true enumeration (steps, distinct options, conditions, fields): one item per line, `- ` before each, `1. ` where the order matters, never nested. `**bold**` sparingly, for the one term a reader must not miss. In a long statement with distinct parts, a `#### ` sub-heading on its own line before each part. Identifiers, paths and commands in backticks. Nothing else: no tables, links, quotes, code blocks or HTML. A short statement stays plain paragraphs."

// SummaryLayout is how a summary takes Layout, word for word in every
// prompt that writes one: the summary is the page's longest text and the
// first a reader sees, never one block of prose.
const SummaryLayout = "The summary takes the same layout: each part of its order under its own `#### ` sub-heading, a short German title, in that order; under it one or more paragraphs; a part that is a true enumeration (the repositories and their roles, a repository's main parts) as a `- ` list, one item each, its name first in backticks; `**bold**` for the one term of a part a reader must not miss. A long summary is never one block of prose; only a summary of a few sentences stays plain paragraphs."
