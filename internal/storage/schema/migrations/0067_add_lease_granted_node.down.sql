-- Reverse of 0067: drop granted_node again. leases is dolt_ignored
-- (clone-local), so this only forgets granting-replica provenance on rows
-- this clone already holds. Guarded so a workspace that never gained the
-- column (or has no leases table at all) no-ops.
-- Pair it with a binary rollback: a 0067-era bd reads/writes granted_node
-- unconditionally in issueops/lease.go, so against a downgraded table every
-- claim/heartbeat/list would error again.
SET @has_col = IF(
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.TABLES
        WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'leases') > 0
    AND
    (SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
        WHERE TABLE_SCHEMA = DATABASE()
          AND TABLE_NAME = 'leases'
          AND COLUMN_NAME = 'granted_node') > 0,
    1, 0
);
SET @sql = IF(@has_col = 1,
    'ALTER TABLE leases DROP COLUMN granted_node',
    'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
