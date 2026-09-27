-- Which providers exist is the control server's call, so the column is free text.
ALTER TABLE llm_keys DROP CONSTRAINT llm_keys_provider_check;
