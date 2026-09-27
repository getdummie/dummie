DELETE FROM llm_keys WHERE provider <> 'zai';
ALTER TABLE llm_keys ADD CONSTRAINT llm_keys_provider_check CHECK (provider IN ('zai'));
