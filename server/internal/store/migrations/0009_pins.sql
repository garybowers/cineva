-- Quick sign-in PINs: an optional 4–6 digit code per user (argon2id
-- hashed like passwords) for the household profile picker. A user may
-- have a PIN, a password, or both; password_hash '' means password
-- sign-in is not possible for that account.

ALTER TABLE users ADD COLUMN pin_hash TEXT NOT NULL DEFAULT '';
