-- Backfill leases.granted_node for databases that ran the in-place upgrade
-- path through 0055 (bd-red8u regression).
--
-- ignored/0016_add_lease_granted_node claims "materialized by ignored/0012 on
-- fresh clones and by synced 0055 on in-place upgrades," but 0055's
-- CREATE TABLE __temp__leases never actually gained a granted_node column --
-- it creates leases with only (issue_id, holder, granted_at, lease_expires_at,
-- heartbeat_at). Any database that sequentially migrated through 0055 as
-- currently written is left permanently missing the column that
-- issueops/lease.go unconditionally reads and writes (l.granted_node), so
-- every claim/heartbeat/list on such a database fails with "table \"leases\"
-- does not have column \"granted_node\"" the moment it reaches that code path.
--
-- Fresh clones are unaffected: their schema_migrations cursor arrives
-- at-latest and they materialize leases (with granted_node) via ignored/0012
-- directly, never running 0055 or this migration's ALTER at all.
--
-- Same guard shape as ignored/0016: a no-op on a workspace with no leases
-- table yet, and a no-op on one that already carries the column (e.g. a
-- database seeded out-of-band with the ignored-track shape).
SET @needs_add = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'leases') > 0
    AND
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'leases'
          AND COLUMN_NAME = 'granted_node') = 0,
    1, 0
);
SET @sql = IF(@needs_add = 1,
    'ALTER TABLE leases ADD COLUMN granted_node VARCHAR(255) NOT NULL DEFAULT ''''',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
