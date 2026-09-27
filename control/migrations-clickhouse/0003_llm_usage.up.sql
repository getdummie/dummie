CREATE TABLE llm_usage
(
    timestamp          DateTime64(6, 'UTC') CODEC(Delta, ZSTD(1)),

    user_id            UUID,
    vm_id              UUID,
    provider           LowCardinality(String),
    model              LowCardinality(String),

    status             UInt16,
    duration_ms        UInt32,

    input_tokens       UInt64,
    output_tokens      UInt64,
    cache_read_tokens  UInt64,
    cache_write_tokens UInt64
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
ORDER BY (user_id, timestamp)
SETTINGS index_granularity = 8192
