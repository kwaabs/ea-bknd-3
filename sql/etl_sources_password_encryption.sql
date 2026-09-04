-- Replaces app.etl_sources.password_env_var with an encrypted-at-rest
-- password column, managed entirely through the ETL admin UI.
--
-- Why the switch: password_env_var meant a source's credential lived in
-- the server's process environment, set once at deploy time — fine for a
-- password that basically never changes (this repo's LDAP_BIND_PASS is
-- the same shape), but these source system passwords rotate on a ~30-day
-- policy. Under password_env_var, every rotation meant an ops ticket to
-- edit the server's environment and restart the process, once per source,
-- every month. An encrypted column lets whoever manages a source rotate
-- its password from the Edit Source form itself — no server access, no
-- restart, no redeploy.
--
-- This does NOT reopen the "don't store third-party credentials in our
-- own app's Postgres" concern password_env_var was chosen to avoid: the
-- password is encrypted with pgcrypto (PGP symmetric, AES-256) under a
-- key that itself still lives only in the server's environment
-- (ETL_CREDENTIALS_ENCRYPTION_KEY — see internal/config/config.go), never
-- in this table. A raw dump/leak of app.etl_sources on its own is
-- ciphertext, not a usable credential. The operational surface shrinks
-- from "one env var per source, rotated every ~30 days" to "one env var
-- total, rotated rarely" — a meaningfully smaller and less frequent
-- manual-ops burden, not a weaker security boundary.
--
-- pgcrypto is already enabled by sql/etl_engine.sql.

ALTER TABLE app.etl_sources
    ADD COLUMN IF NOT EXISTS password_encrypted bytea;

ALTER TABLE app.etl_sources
    DROP COLUMN IF EXISTS password_env_var;

-- ---------------------------------------------------------------------
-- Setting/rotating a source's password directly in SQL (the app's own
-- Edit Source form does this the same way, via pgp_sym_encrypt, using
-- the ETL_CREDENTIALS_ENCRYPTION_KEY the server has configured):
-- ---------------------------------------------------------------------
-- UPDATE app.etl_sources
-- SET password_encrypted = pgp_sym_encrypt('the-new-password', 'the-same-key-as-ETL_CREDENTIALS_ENCRYPTION_KEY'),
--     updated_at = now()
-- WHERE name = 'oracle_finance';
