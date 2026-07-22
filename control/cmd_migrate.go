package main

import (
  "context"
  "errors"
  "fmt"
  "net/url"
  "os"
  "regexp"
  "sort"
  "strconv"

  "github.com/golang-migrate/migrate/v4"
  _ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers scheme "pgx5"
  _ "github.com/golang-migrate/migrate/v4/source/file"     // registers scheme "file"
  "github.com/urfave/cli/v3"
)

const migrationsDir = "migrations"

func migrateCommand() *cli.Command {
  return &cli.Command{
    Name:  "migrate",
    Usage: "apply or revert database migrations",
    Commands: []*cli.Command{
      {
        Name:      "up",
        Usage:     "apply migrations (optionally up to a target version, e.g. `migrate up 0002`)",
        Arguments: []cli.Argument{&cli.StringArg{Name: "target"}},
        Action: func(ctx context.Context, cmd *cli.Command) error {
          return runMigrate(true, cmd.Args().First())
        },
      },
      {
        Name:      "down",
        Usage:     "revert migrations (optionally down to a target version, e.g. `migrate down 0003`)",
        Arguments: []cli.Argument{&cli.StringArg{Name: "target"}},
        Action: func(ctx context.Context, cmd *cli.Command) error {
          return runMigrate(false, cmd.Args().First())
        },
      },
    },
  }
}

// newMigrator builds a *migrate.Migrate from DATABASE_URL. The pgx/v5 migrate
// driver registers the scheme "pgx5", so we rewrite the DSN scheme accordingly.
func newMigrator() (*migrate.Migrate, error) {
  dsn := os.Getenv("DATABASE_URL")
  if dsn == "" {
    return nil, errors.New("DATABASE_URL is not set")
  }
  u, err := url.Parse(dsn)
  if err != nil {
    return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
  }
  u.Scheme = "pgx5"
  return migrate.New("file://"+migrationsDir, u.String())
}

// versionsInDir returns the sorted, de-duplicated migration versions on disk.
func versionsInDir() ([]uint, error) {
  entries, err := os.ReadDir(migrationsDir)
  if err != nil {
    return nil, err
  }
  re := regexp.MustCompile(`^(\d+)_.*\.(up|down)\.sql$`)
  seen := map[uint]struct{}{}
  for _, e := range entries {
    m := re.FindStringSubmatch(e.Name())
    if m == nil {
      continue
    }
    n, _ := strconv.ParseUint(m[1], 10, 64)
    seen[uint(n)] = struct{}{}
  }
  out := make([]uint, 0, len(seen))
  for v := range seen {
    out = append(out, v)
  }
  sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
  return out, nil
}

// runMigrate drives migrations one step at a time so each applied/reverted
// migration produces its own log line. targetArg "" means "all".
func runMigrate(up bool, targetArg string) error {
  m, err := newMigrator()
  if err != nil {
    return err
  }
  defer func() { _, _ = m.Close() }()

  versions, err := versionsInDir()
  if err != nil {
    return err
  }

  // Current applied version (0 == none applied yet).
  cur, dirty, err := m.Version()
  if errors.Is(err, migrate.ErrNilVersion) {
    cur = 0
  } else if err != nil {
    return err
  }
  if dirty {
    return fmt.Errorf("database is dirty at version %04d; resolve manually", cur)
  }

  // target: 0 means "all".
  var target uint
  if targetArg != "" {
    n, perr := strconv.ParseUint(targetArg, 10, 64)
    if perr != nil {
      return fmt.Errorf("invalid target version %q: %w", targetArg, perr)
    }
    target = uint(n)
  }

  if up {
    for _, v := range versions {
      if v <= cur {
        continue
      }
      if target != 0 && v > target {
        break
      }
      fmt.Printf("Running migration %04d\n", v)
      if err := m.Steps(1); err != nil {
        if errors.Is(err, migrate.ErrNoChange) {
          break
        }
        return err
      }
    }
    return nil
  }

  // down: revert from current downward, stopping before target (exclusive).
  for i := len(versions) - 1; i >= 0; i-- {
    v := versions[i]
    if v > cur {
      continue
    }
    if v <= target { // target 0 => revert everything
      break
    }
    fmt.Printf("Running migration %04d\n", v)
    if err := m.Steps(-1); err != nil {
      if errors.Is(err, migrate.ErrNoChange) {
        break
      }
      return err
    }
  }
  return nil
}
