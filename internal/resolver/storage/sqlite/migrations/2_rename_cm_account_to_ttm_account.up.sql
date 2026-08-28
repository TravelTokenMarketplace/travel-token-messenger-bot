-- Rename the account column to match the TTM naming.
--
-- This is a separate migration rather than an edit to migration 1 because the
-- migration tool records only a version number, with no checksum of what that
-- version contained. Editing an already-applied migration is therefore
-- invisible to it: a database that already has version 1 is never revisited,
-- so it keeps the old column while the code expects the new one.
ALTER TABLE bots RENAME COLUMN cm_account TO ttm_account;
