package store

import "testing"

func TestMigrationVersionAndSplitSQL(t *testing.T) {
	version, err := migrationVersion("001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d", version)
	}
	if _, err := migrationVersion("init.sql"); err == nil {
		t.Fatal("expected missing version")
	}
	parts := splitSQL("-- comment\nCREATE TABLE t (id INT);\n\nINSERT INTO t VALUES (1);\n")
	if len(parts) != 2 {
		t.Fatalf("statements = %#v", parts)
	}
}
