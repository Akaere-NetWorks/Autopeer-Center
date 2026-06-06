-- Prevent duplicate registrations of the same authenticator credential.
-- UpdateSignCount and TouchCredential both query by credential_id; without
-- uniqueness, a retried RegisterFinish could create two rows for the same
-- credential, causing both to be updated on every subsequent login.
ALTER TABLE passkey_credentials
    ADD CONSTRAINT passkey_credentials_credential_id_unique UNIQUE (credential_id);
