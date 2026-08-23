-- Replaces the NOTIFY_EMAILS env var (backend) and the hardcoded
-- src/lib/notify-config.ts array (frontend, ea-ftnd-2) with one shared
-- table — previously the same allowlist had to be edited in two places in
-- two repos and redeployed to change who could reach admin-gated routes
-- (meters admin, express-feeders admin, announcements). Now it's one row
-- insert/delete, no redeploy needed.
--
-- Run this BEFORE deploying the backend code that reads this table
-- (internal/notifyemail and its callers in internal/meters and
-- internal/announcements) — those will fail every admin-gated request
-- with a DB error until this table exists. This is not a hard app-wide
-- outage (only admin-only routes are gated by it), but don't leave the
-- gap open longer than necessary.

CREATE TABLE IF NOT EXISTS app.notify_emails (
    email      text PRIMARY KEY,
    added_by   text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Seed with the exact same defaults NOTIFY_EMAILS and notify-config.ts
-- already had, so cutover doesn't lock out anyone who currently has
-- access. ON CONFLICT DO NOTHING makes this safe to re-run.
INSERT INTO app.notify_emails (email) VALUES
    ('jdanso@ecggh.com'),
    ('yadofo@ecggh.com')
ON CONFLICT (email) DO NOTHING;
