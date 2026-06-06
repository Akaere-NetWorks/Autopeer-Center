ALTER TABLE webauthn_sessions
    ADD CONSTRAINT webauthn_sessions_asn_type_unique UNIQUE (asn, type);
