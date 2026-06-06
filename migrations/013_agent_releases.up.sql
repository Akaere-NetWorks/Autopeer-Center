CREATE TABLE IF NOT EXISTS agent_releases (
    version     TEXT NOT NULL,
    os          TEXT NOT NULL DEFAULT 'linux',
    arch        TEXT NOT NULL DEFAULT 'amd64',
    sha256      TEXT NOT NULL,
    size        BIGINT NOT NULL,
    path        TEXT NOT NULL,
    uploaded_by TEXT,
    uploaded_at TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (version, os, arch)
);
