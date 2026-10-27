-- API keys usable as a password in music apps (server/docs/subsonic.md).
-- Subsonic's salt-and-token sign-in sends md5(password + salt), so the
-- server must be able to read the key back: a key made "for apps" keeps a
-- copy sealed with AES-GCM under secrets_key, generated once per server.
-- A key without one is a plain bearer key as before: hashed, never read.
ALTER TABLE api_keys ADD COLUMN secret_enc BLOB;
ALTER TABLE server_identity ADD COLUMN secrets_key BLOB;
