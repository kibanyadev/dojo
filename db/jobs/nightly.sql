-- The nightly job (design 3.7): runs as the migration role, which owns the tables.
\set ON_ERROR_STOP on
SELECT prune_telemetry() AS telemetry_events_deleted \gset
\echo prune: telemetry_events deleted :telemetry_events_deleted
SELECT prune_admin_audit() AS admin_audit_deleted \gset
\echo prune: admin_audit deleted :admin_audit_deleted
SELECT prune_events() AS events_deleted \gset
\echo prune: events deleted :events_deleted
SELECT prune_pending_commits() AS pending_commits_deleted \gset
\echo prune: pending_commits deleted :pending_commits_deleted
SELECT prune_ai_reviews() AS ai_reviews_deleted \gset
\echo prune: ai_reviews deleted :ai_reviews_deleted
