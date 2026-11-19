-- Initial schema. Months are TEXT 'YYYY-MM'; timestamps are TEXT ISO-8601 UTC
-- (RFC 3339). Money is INTEGER øre; shares are INTEGER basis points (5000 = 50 %).

CREATE TABLE users (
    id           INTEGER PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,     -- login handle used by the CLI, e.g. "kari"
    display_name TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

CREATE TABLE passkeys (
    id            INTEGER PRIMARY KEY,
    user_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id BLOB NOT NULL UNIQUE,
    public_key    BLOB NOT NULL,
    sign_count    INTEGER NOT NULL DEFAULT 0,
    transports    TEXT NOT NULL DEFAULT '[]', -- JSON array, e.g. ["internal","hybrid"]
    -- WebAuthn authenticator flags from registration. go-webauthn checks the stored
    -- backup-eligible bit against every login assertion, so it must be kept.
    flags         INTEGER NOT NULL DEFAULT 0,
    aaguid        BLOB,
    label         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    last_used_at  TEXT
);
CREATE INDEX passkeys_user ON passkeys (user_id);

-- One-time links printed by `finance enroll`. Only the sha256 of the token is stored.
CREATE TABLE enrollment_tokens (
    token_hash BLOB PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    used_at    TEXT
);

-- Login sessions. Only the sha256 of the cookie value is stored.
CREATE TABLE sessions (
    token_hash BLOB PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

-- Net monthly income, effective-dated: the value for month M is the row with the
-- greatest valid_from <= M.
CREATE TABLE incomes (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id),
    valid_from TEXT NOT NULL CHECK (valid_from GLOB '[0-9][0-9][0-9][0-9]-[01][0-9]'
                                    AND substr(valid_from, 6, 2) BETWEEN '01' AND '12'),
    amount_ore INTEGER NOT NULL CHECK (amount_ore >= 0),
    note       TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users (id),
    created_at TEXT NOT NULL,
    UNIQUE (user_id, valid_from)
);

CREATE TABLE expenses (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    category   TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL CHECK (kind IN ('shared', 'private')),
    owner_id   INTEGER REFERENCES users (id),          -- private only
    end_month  TEXT CHECK (end_month IS NULL
                           OR (end_month GLOB '[0-9][0-9][0-9][0-9]-[01][0-9]'
                               AND substr(end_month, 6, 2) BETWEEN '01' AND '12')),
    created_by INTEGER NOT NULL REFERENCES users (id),
    created_at TEXT NOT NULL,
    CHECK ((kind = 'private') = (owner_id IS NOT NULL))
);

-- The terms of an expense from valid_from until the next version.
CREATE TABLE expense_versions (
    id         INTEGER PRIMARY KEY,
    expense_id INTEGER NOT NULL REFERENCES expenses (id) ON DELETE CASCADE,
    valid_from TEXT NOT NULL CHECK (valid_from GLOB '[0-9][0-9][0-9][0-9]-[01][0-9]'
                                    AND substr(valid_from, 6, 2) BETWEEN '01' AND '12'),
    amount_ore INTEGER NOT NULL CHECK (amount_ore >= 0),  -- per occurrence
    due_months INTEGER NOT NULL DEFAULT 4095 CHECK (due_months BETWEEN 1 AND 4095), -- bit 0 = January
    payer_id   INTEGER REFERENCES users (id),             -- NULL = paid from the felleskonto
    created_by INTEGER NOT NULL REFERENCES users (id),
    created_at TEXT NOT NULL,
    UNIQUE (expense_id, valid_from)
);

-- Split of a shared expense version; must sum to 10000 (enforced in Go on write).
CREATE TABLE expense_version_shares (
    version_id INTEGER NOT NULL REFERENCES expense_versions (id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users (id),
    share_bp   INTEGER NOT NULL CHECK (share_bp BETWEEN 0 AND 10000),
    PRIMARY KEY (version_id, user_id)
) WITHOUT ROWID;
