package main

import (
	"fmt"
	"os"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

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
