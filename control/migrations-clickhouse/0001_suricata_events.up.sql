CREATE TABLE suricata_events
(
    timestamp       DateTime64(6, 'UTC') CODEC(Delta, ZSTD(1)),
    event_type      LowCardinality(String),
    flow_id         UInt64 CODEC(ZSTD(1)),
    pkt_src         LowCardinality(String),
    direction       LowCardinality(String),
    src_ip          IPv6 CODEC(ZSTD(1)),
    dest_ip         IPv6 CODEC(ZSTD(1)),
    src_port        UInt16 CODEC(T64, ZSTD(1)),
    dest_port       UInt16 CODEC(T64, ZSTD(1)),
    proto           LowCardinality(String),
    ip_v            UInt8 CODEC(ZSTD(1)),
    app_proto       LowCardinality(String) DEFAULT '',

    alert__action        LowCardinality(String) DEFAULT '',
    alert__gid           UInt16 DEFAULT 0,
    alert__signature_id  UInt32 DEFAULT 0,
    alert__rev           UInt16 DEFAULT 0,
    alert__signature     LowCardinality(String) DEFAULT '',
    alert__category      LowCardinality(String) DEFAULT '',
    alert__severity      UInt8 DEFAULT 0,

    flow__pkts_toserver  UInt64 DEFAULT 0 CODEC(T64, ZSTD(1)),
    flow__pkts_toclient  UInt64 DEFAULT 0 CODEC(T64, ZSTD(1)),
    flow__bytes_toserver UInt64 DEFAULT 0 CODEC(T64, ZSTD(1)),
    flow__bytes_toclient UInt64 DEFAULT 0 CODEC(T64, ZSTD(1)),
    flow__start          Nullable(DateTime64(6, 'UTC')) CODEC(Delta, ZSTD(1)),

    drop__reason    LowCardinality(String) DEFAULT '',
    drop__len       UInt16 DEFAULT 0,
    drop__ttl       UInt8  DEFAULT 0,
    drop__tos       UInt8  DEFAULT 0,
    drop__ipid      UInt16 DEFAULT 0 CODEC(ZSTD(1)),
    drop__udplen    UInt16 DEFAULT 0,
    drop__tcpseq    UInt32 DEFAULT 0 CODEC(ZSTD(1)),
    drop__tcpack    UInt32 DEFAULT 0 CODEC(ZSTD(1)),
    drop__tcpwin    UInt16 DEFAULT 0,
    drop__tcpres    UInt8  DEFAULT 0,
    drop__tcpurgp   UInt16 DEFAULT 0,
    drop__syn       Bool DEFAULT false,
    drop__ack       Bool DEFAULT false,
    drop__psh       Bool DEFAULT false,
    drop__rst       Bool DEFAULT false,
    drop__urg       Bool DEFAULT false,
    drop__fin       Bool DEFAULT false,

    dns__version    UInt8 DEFAULT 0,
    dns__type       LowCardinality(String) DEFAULT '',
    dns__id         UInt16 DEFAULT 0,
    dns__tx_id      UInt16 DEFAULT 0,
    dns__flags      LowCardinality(String) DEFAULT '',
    dns__rd         Bool DEFAULT false,
    dns__opcode     UInt8 DEFAULT 0,
    dns__rcode      LowCardinality(String) DEFAULT '',
    dns__queries    Nested(rrname String, rrtype LowCardinality(String)),

    tls__sni        String DEFAULT '' CODEC(ZSTD(1)),
    tls__version    LowCardinality(String) DEFAULT '',
    http__hostname  String DEFAULT '' CODEC(ZSTD(1)),
    http__url       String DEFAULT '' CODEC(ZSTD(1)),
    http__method    LowCardinality(String) DEFAULT '',
    http__status    UInt16 DEFAULT 0,

    anomaly__type   LowCardinality(String) DEFAULT '',
    anomaly__event  LowCardinality(String) DEFAULT '',
    anomaly__layer  LowCardinality(String) DEFAULT '',

    domain          String MATERIALIZED coalesce(
                        nullIf(dns__queries.rrname[1], ''),
                        nullIf(tls__sni, ''),
                        nullIf(http__hostname, ''),
                        ''
                    ) CODEC(ZSTD(1)),

    INDEX idx_flow   flow_id             TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_sid    alert__signature_id TYPE set(64)            GRANULARITY 4,
    INDEX idx_qname  dns__queries.rrname TYPE bloom_filter(0.01) GRANULARITY 4,
    INDEX idx_domain domain              TYPE bloom_filter(0.01) GRANULARITY 4
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(timestamp)
ORDER BY (event_type, dest_ip, dest_port, timestamp)
TTL toDateTime(timestamp) + INTERVAL 1 YEAR
SETTINGS index_granularity = 8192,
         ttl_only_drop_parts = 1
