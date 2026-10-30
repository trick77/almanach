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
