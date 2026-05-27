-- Rename triggers.webhook_secret_hash → webhook_secret_ciphertext. v1 stores
-- the HMAC secret as AES-GCM ciphertext (key from API_SECRETS_KEY); the
-- "_hash" suffix from 002 was a misnomer since HMAC verification needs the
-- plaintext at request time. KMS-wrapping is the upgrade path for v1.5.

BEGIN;

ALTER TABLE triggers
    RENAME COLUMN webhook_secret_hash TO webhook_secret_ciphertext;

COMMIT;
