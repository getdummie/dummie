-- The RDP password a user types into their native client is derived, not stored:
-- HMAC(proxy auth secret, 'rdp:' || vms.id || ':' || rdp_nonce). Rotating the
-- credential means replacing this nonce, so no password or hash lives in the row.
ALTER TABLE vms ADD COLUMN rdp_nonce TEXT NOT NULL DEFAULT gen_random_uuid()::text;
