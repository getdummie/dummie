package main

import (
  "fmt"
  "os"

  "github.com/ClickHouse/clickhouse-go/v2"
  "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// openClickHouse builds a lazy connection from CLICKHOUSE_URL, or returns a nil
// conn when it is unset. Same shape as the pgx pool in runServe: Open does not
// dial, so an unreachable clickhouse never blocks startup, and the healthcheck
// is what discovers it. A nil conn makes the healthcheck report clickhouse:false
// rather than panicking, which is the right answer for a host that is not
// collecting events.
func openClickHouse() (driver.Conn, error) {
  dsn := os.Getenv("CLICKHOUSE_URL")
  if dsn == "" {
    return nil, nil
  }
  opts, err := clickhouse.ParseDSN(dsn)
  if err != nil {
    return nil, fmt.Errorf("invalid CLICKHOUSE_URL: %w", err)
  }
  conn, err := clickhouse.Open(opts)
  if err != nil {
    return nil, fmt.Errorf("could not open clickhouse: %w", err)
  }
  return conn, nil
}
