package store

import (
	"path/filepath"
	"testing"

	"github.com/trick77/ship/kit/schemadoc"
)

// TestSchemaDoc keeps docs/schema.md in step with the migrations and the
// model.go row types. A file database, not :memory:: the pool would hand each
// connection its own empty database.
func TestSchemaDoc(t *testing.T) {
	s := testStore(t)
	sc, err := schemadoc.Describe(t.Context(), s.db, schemadoc.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	es, err := schemadoc.Entities(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := schemadoc.Render(sc, es)
	if err != nil {
		t.Fatal(err)
	}
	schemadoc.Golden(t, doc, filepath.Join("..", "..", "..", "docs", "schema.md"))
}
