CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    checksum TEXT NOT NULL CHECK (length(checksum) = 64)
) STRICT;

CREATE TABLE entitlements (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    provider TEXT NOT NULL CHECK (length(trim(provider)) > 0),
    target TEXT NOT NULL CHECK (length(trim(target)) > 0),
    role TEXT NOT NULL CHECK (length(trim(role)) > 0),
    max_ttl_ns INTEGER NOT NULL CHECK (max_ttl_ns > 0)
) STRICT;

CREATE TABLE grants (
    id TEXT PRIMARY KEY NOT NULL CHECK (length(trim(id)) > 0),
    entitlement_id TEXT NOT NULL REFERENCES entitlements(id),
    state TEXT NOT NULL CHECK (state IN (
        'requested', 'pending_approval', 'approved', 'denied',
        'granting', 'active', 'revoking', 'revoked'
    )),
    version INTEGER NOT NULL CHECK (version > 0),
    expires_at_ns INTEGER NOT NULL,
    payload BLOB NOT NULL
) STRICT;
CREATE INDEX grants_recovery ON grants(state, id);

CREATE TABLE events (
    sequence INTEGER PRIMARY KEY CHECK (sequence > 0),
    grant_id TEXT NOT NULL REFERENCES grants(id),
    body BLOB NOT NULL,
    previous_hash TEXT NOT NULL CHECK (length(previous_hash) = 64),
    hash TEXT NOT NULL CHECK (length(hash) = 64)
) STRICT;
CREATE INDEX events_grant ON events(grant_id, sequence);

CREATE TABLE chain_head (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    sequence INTEGER NOT NULL CHECK (sequence >= 0),
    hash TEXT NOT NULL CHECK (length(hash) = 64)
) STRICT;
INSERT INTO chain_head VALUES (1, 0, '0000000000000000000000000000000000000000000000000000000000000000');

CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
