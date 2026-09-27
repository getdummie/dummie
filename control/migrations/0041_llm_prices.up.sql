-- USD per token, from LiteLLM's public price list. Refreshed daily; cost is
-- worked out when usage is read, so a price change applies to past usage too.
CREATE TABLE llm_prices (
    model                 TEXT PRIMARY KEY,
    input_per_token       DOUBLE PRECISION NOT NULL,
    output_per_token      DOUBLE PRECISION NOT NULL,
    cache_read_per_token  DOUBLE PRECISION NOT NULL,
    cache_write_per_token DOUBLE PRECISION NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
