CREATE TABLE request_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    method TEXT,
    path TEXT,
    status_code INTEGER,
    ip TEXT,
    duration_ms INTEGER,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_request_logs_created_at ON request_logs(created_at);
