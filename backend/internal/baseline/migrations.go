package baseline

import (
	"path"
	"regexp"
	"strings"
)

// Schema migrations say how a database got where it is, never what it is,
// and not every store has a schema: the docs learn the data model from the
// code that uses it (entities, the store's queries and types), so a run
// reads no migration (user, 2026-11-08: not interesting, out of the docs).
// A schema dump is none: it says what the database is.

// migrationDirs are folders whose migration-named files are schema
// migrations wherever they sit (plain SQL tools, ORMs, Flyway's
// db/migration). Rails' db/migrate and Alembic's alembic/versions count
// only so, and a Liquibase changelog only under a db or liquibase folder:
// a Go package "migrate" is the code that runs them, a "changelogs"
// folder of YAML can be release notes.
var migrationDirs = map[string]bool{"migrations": true, "migration": true}

// liquibaseExt is what a Liquibase changelog is written in.
var liquibaseExt = map[string]bool{".xml": true, ".yaml": true, ".yml": true, ".sql": true, ".json": true}

var (
	// flywayName is a Flyway versioned (V1__, V1.2__, V1_2__) or repeatable
	// (R__) migration.
	flywayName = regexp.MustCompile(`^(V\d+([._]\d+)*|R)__.+\.sql$`)
	// migrationName is a file named as one step of a migration folder: a
	// version or timestamp first (0001_init.sql, 20240101_x.rb,
	// 1700000000-Users.ts, 000001_init.up.sql), or any SQL.
	migrationName = regexp.MustCompile(`^(\d|V\d)`)
	// dumpName is a schema dump (sqlc, dbmate, Rails, pg_dump).
	dumpName = regexp.MustCompile(`^(schema|structure)\.(sql|rb)$`)
)

// MigrationFile reports whether a path is a schema migration by its name.
func MigrationFile(p string) bool {
	base := path.Base(p)
	if dumpName.MatchString(base) {
		return false
	}
	ext := strings.ToLower(path.Ext(base))
	dirs := strings.Split(path.Dir(p), "/")
	liquibase := false
	for i, d := range dirs {
		d = strings.ToLower(d)
		parent := ""
		if i > 0 {
			parent = strings.ToLower(dirs[i-1])
		}
		switch {
		case migrationDirs[d] && (ext == ".sql" || migrationName.MatchString(base)):
			return true
		case d == "migrate" && parent == "db" && ext == ".rb", d == "versions" && parent == "alembic" && ext == ".py":
			return true
		case d == "liquibase" && liquibaseExt[ext]:
			return true
		case (d == "changelog" || d == "changelogs") && liquibase && liquibaseExt[ext]:
			return true
		}
		liquibase = liquibase || d == "db"
	}
	return flywayName.MatchString(base)
}
