CREATE TABLE dns_queries
(
    timestamp DateTime64(6, 'UTC') CODEC(Delta, ZSTD(1)),

    src_ip    IPv6 CODEC(ZSTD(1)),

    qname     String CODEC(ZSTD(1)),
    qtype     LowCardinality(String),

    rcode     LowCardinality(String),

    INDEX idx_qname qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
ORDER BY (src_ip, timestamp)
TTL toDateTime(timestamp) + INTERVAL 1 YEAR
SETTINGS index_granularity = 8192,
         ttl_only_drop_parts = 1
