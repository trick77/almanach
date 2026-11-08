package baseline

import (
	"reflect"
	"testing"

	"github.com/trick77/almanach/internal/gitrepo"
)

func TestMigrationFile(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/store/migrations/0001_init.sql":                                true,
		"migrations/000001_init.up.sql":                                          true,
		"src/main/resources/db/migration/V2__orders.sql":                         true,
		"src/main/resources/sql/V1.1__orders.sql":                                true, // Flyway, a custom folder
		"src/main/resources/sql/R__views.sql":                                    true,
		"db/migrate/20240101_create_users.rb":                                    true,
		"lib/persistence/src/main/resources/db/changelog/master.xml":             true,
		"db/changelog/changes/001-users.yaml":                                    true,
		"src/main/resources/config/liquibase/changelog/20240101_added_Order.xml": true, // JHipster
		"alembic/versions/3f2a_add_orders.py":                                    true,
		"orders/migrations/0002_auto.py":                                         true,
		"src/migration/1700000000-Users.ts":                                      true,
		"db/schema.rb":                                                           false, // a dump: what the database is
		"db/migrations/schema.sql":                                               false,
		"internal/migrate/migrate.go":                                            false, // the code that runs them
		"internal/store/migrate.go":                                              false,
		"internal/store/migrations/migrations.go":                                false, // the embed runner
		"internal/db/migrate/migrate.go":                                         false,
		"src/main/java/com/acme/migration/CustomerMigrationJob.java":             false, // a feature
		"orders/migrations/__init__.py":                                          false,
		"docs/changelog/v1.2.md":                                                 false, // release notes
		"changelogs/unreleased/fix.yml":                                          false,
		"internal/store/migrations/README.md":                                    false,
		"api/versions/v1.go":                                                     false,
		"src/main/java/a/entity/Order.java":                                      false,
	} {
		if got := MigrationFile(p); got != want {
			t.Errorf("MigrationFile(%q) = %v, want %v", p, got, want)
		}
	}
}

// Migrations go, whatever holds the schema; the code that uses the data,
// and a schema dump, stay.
func TestFilesLeaveMigrationsOut(t *testing.T) {
	e := func(p string) gitrepo.Entry { return gitrepo.Entry{Path: p, Mode: "100644", Size: 100} }
	var got []string
	for _, f := range Files([]gitrepo.Entry{e("cmd/app/main.go"), e("internal/store/store.go"), e("internal/store/migrations/0002_more.sql"),
		e("internal/store/migrations/0001_init.sql"), e("internal/store/migrations/migrations.go"), e("db/schema.sql")}) {
		got = append(got, f.Path)
	}
	if want := []string{"cmd/app/main.go", "db/schema.sql", "internal/store/migrations/migrations.go", "internal/store/store.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Files = %v, want %v", got, want)
	}
}
