//go:build cgo

package embeddeddolt_test

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/storage/schema"
)

// TestEmbeddedMigrateHealsCollidedVersionedBeadsSchema reproduces a real
// production incident: between acb77fe89 (2026-08-25) and its revert
// d26174b62 (2026-09-20), this repo briefly shipped a LOCAL
// 0067_add_lease_granted_node.up.sql that collided with upstream's own,
// unrelated 0067_add_versioned_beads_schema.up.sql (both claimed version 67).
// A database migrated by a `bd` built in that window recorded
// schema_migrations version=67 under the LOCAL file's content_hash — the
// revert cleared the collision going forward, but cannot rewrite a row a
// store already committed. Every later binary compares only the integer
// version (never content_hash) to decide what's pending, so it treats 0067
// as already applied and skips its SQL text forever: issue_versions,
// store_epoch and issues/wisps.current_revision are never created. The pass
// then reaches 0068, whose own guard misreads "table does not exist" as
// "column does not exist" and fires an ALTER against a table that was never
// created — Error 1146: table not found: issue_versions.
//
// This seeds exactly that shape (a v67 cursor row with NO 0067 artifacts,
// the state a real affected store carries after the collision, without
// depending on the now-deleted local migration file to produce it) and
// proves MigrateUp converges to a healthy, complete schema instead of
// crashing on 0068.
func TestEmbeddedMigrateHealsCollidedVersionedBeadsSchema(t *testing.T) {
	requireEmbedded(t)
	ctx := context.Background()

	// seedMainSchemaAt(66) gives a clean, committed store one version below
	// the collision. Simulate the collided v67 row by hand — a real affected
	// store's version=67 came from a different (now-deleted) migration file
	// entirely, so recreating it from THIS binary's own 0067 text would not
	// reproduce the bug shape.
	dataDir := seedMainSchemaAt(t, ctx, 66)
	conn, closeConn := openPinnedConn(t, ctx, dataDir)

	if _, err := conn.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, content_hash) VALUES (67, 'collided-local-lease-migration-hash')"); err != nil {
		t.Fatalf("seed collided v67 cursor row: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD('-A')"); err != nil {
		t.Fatalf("stage collided v67 row: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', 'test: simulate collided v67 cursor row')"); err != nil {
		t.Fatalf("commit collided v67 row: %v", err)
	}
	closeConn()

	// Sanity: the collision is seeded, none of 0067's artifacts exist yet.
	conn, closeConn = openPinnedConn(t, ctx, dataDir)
	for _, table := range []string{"issue_versions", "store_epoch"} {
		var count int
		if err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", table,
		).Scan(&count); err != nil {
			t.Fatalf("pre-check %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("pre-check: %s already exists before MigrateUp, seed did not reproduce the collision shape", table)
		}
	}
	closeConn()

	// The actual repro: a plain incremental MigrateUp, the same path
	// `bd migrate` takes, must NOT die with "table not found: issue_versions"
	// at 0068.
	conn, closeConn = openPinnedConn(t, ctx, dataDir)
	defer closeConn()
	if _, err := schema.MigrateUp(ctx, conn); err != nil {
		t.Fatalf("MigrateUp on a collided-v67 store: %v", err)
	}

	for _, table := range []string{"issue_versions", "store_epoch"} {
		var count int
		if err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?", table,
		).Scan(&count); err != nil {
			t.Fatalf("post-check %s: %v", table, err)
		}
		if count == 0 {
			t.Errorf("post-check: %s still missing after MigrateUp healed the collision", table)
		}
	}
	for _, table := range []string{"issues", "wisps"} {
		var count int
		if err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = 'current_revision'", table,
		).Scan(&count); err != nil {
			t.Fatalf("post-check %s.current_revision: %v", table, err)
		}
		if count == 0 {
			t.Errorf("post-check: %s.current_revision still missing after MigrateUp healed the collision", table)
		}
	}

	v, err := scalarVersion(ctx, conn)
	if err != nil {
		t.Fatalf("read final cursor version: %v", err)
	}
	if v != schema.LatestVersion() {
		t.Errorf("final cursor version = %d, want LatestVersion() = %d", v, schema.LatestVersion())
	}
}
