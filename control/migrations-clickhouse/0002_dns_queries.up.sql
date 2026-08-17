-- 0002_dns_queries: what the resolver on each host was asked, and what it said.
--
-- This exists because of a hole the resolver opened in the answer to "what did my
-- VM try to reach that policy stopped?". That question used to be answerable from
-- suricata alone: a blocked packet and the name it was going to are two rows on
-- one flow_id, and the join between them is the denied-domains panel.
--
-- Once guest dns terminates on the gateway, the most common denial by far -- a
-- lookup for a name nobody granted -- never becomes a packet at all. Suricata sees
-- nothing, there is no flow, and the panel goes quiet in exactly the way that
-- reads as "nothing was blocked". These rows are that denial.
--
-- Deliberately not columns on suricata_events. Nothing here has a flow_id or a
-- port, the source is a different process on the host, and the one query that
-- reads both wants them grouped differently.
CREATE TABLE dns_queries
(
    timestamp DateTime64(6, 'UTC') CODEC(Delta, ZSTD(1)),

    -- The guest that asked. IPv6 to match suricata_events, so a caller filtering
    -- both tables writes toIPv6(?) once and does not have to remember which is
    -- which.
    src_ip    IPv6 CODEC(ZSTD(1)),

    -- Lowercased with the root dot stripped, so it compares directly against a
    -- vm_network_targets destination and against suricata_events.domain.
    qname     String CODEC(ZSTD(1)),
    qtype     LowCardinality(String),

    -- REFUSED is the interesting one: it is this fleet's word for "not on the
    -- allowlist". NOERROR rows are kept too -- an allowed lookup is the other half
    -- of the picture, and without it the panel could not tell a guest that was
    -- blocked from one that never asked.
    rcode     LowCardinality(String),

    INDEX idx_qname qname TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
-- src_ip first because every read is one guest's history: the panel is scoped to
-- one VM, and a destroyed VM's address is reissued, so time alone is never the
-- filter.
ORDER BY (src_ip, timestamp)
TTL toDateTime(timestamp) + INTERVAL 1 YEAR
SETTINGS index_granularity = 8192,
         ttl_only_drop_parts = 1
