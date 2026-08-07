package main

import (
  "context"
  "errors"
  "fmt"
  "net/url"
  "os"
  "regexp"
  "slices"
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
      // The argument is read with StringArg, not Args().First(): a declared
      // cli.Argument is consumed during parsing, so Args() is empty by the time
      // the action runs and every target silently became "all".
      {
        Name:      "up",
        Usage:     "apply migrations (optionally up to a target version, e.g. `migrate up 10`)",
        Arguments: []cli.Argument{&cli.StringArg{Name: "target"}},
        Action: func(ctx context.Context, cmd *cli.Command) error {
          return runMigrate(true, cmd.StringArg("target"))
        },
      },
      {
        Name:      "down",
        Usage:     "revert migrations (optionally down to a target version, e.g. `migrate down 10`)",
        Arguments: []cli.Argument{&cli.StringArg{Name: "target"}},
        Action: func(ctx context.Context, cmd *cli.Command) error {
          return runMigrate(false, cmd.StringArg("target"))
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
//
// The target names the last migration whose script runs, in both directions:
//
//	migrate up 10    runs 0010.up last   -> ends at version 10
//	migrate down 10  runs 0010.down last -> ends at version 9
//
// So the number is always "the migration I want executed", rather than meaning
// a version to end at going up and a version to stop above going down.
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
    // A version with no file is a typo, and silently treating it as a bound
    // would run every migration up to it -- the opposite of asking for less.
    if !slices.Contains(versions, target) {
      return fmt.Errorf("no migration %04d in %s/", target, migrationsDir)
    }
  }

  ran := 0

  if up {
    if target != 0 && target <= cur {
      fmt.Printf("Already at version %04d; nothing to apply\n", cur)
      return nil
    }
    for _, v := range versions {
      if v <= cur {
        continue
      }
      if target != 0 && v > target {
        break
      }
      fmt.Printf("Applying %04d\n", v)
      if err := m.Steps(1); err != nil {
        if errors.Is(err, migrate.ErrNoChange) {
          break
        }
        return err
      }
      ran++
    }
    return reportVersion(m, ran, "applied")
  }

  // down: revert from the current version downward, running target's own down
  // last. target 0 => revert everything.
  if target != 0 && target > cur {
    fmt.Printf("Version %04d is not applied (at %04d); nothing to revert\n", target, cur)
    return nil
  }
  for i := len(versions) - 1; i >= 0; i-- {
    v := versions[i]
    if v > cur {
      continue
    }
    if v < target {
      break
    }
    fmt.Printf("Reverting %04d\n", v)
    if err := m.Steps(-1); err != nil {
      if errors.Is(err, migrate.ErrNoChange) {
        break
      }
      return err
    }
    ran++
  }
  return reportVersion(m, ran, "reverted")
}

// reportVersion prints where the database ended up. Worth the extra query: the
// per-migration lines say what was attempted, not what the schema_migrations
// table now says, and those differ if a step stopped early.
func reportVersion(m *migrate.Migrate, ran int, verb string) error {
  v, _, err := m.Version()
  switch {
  case errors.Is(err, migrate.ErrNilVersion):
    fmt.Printf("%d %s; database is now at no version\n", ran, verb)
  case err != nil:
    return err
  default:
    fmt.Printf("%d %s; database is now at version %04d\n", ran, verb, v)
  }
  return nil
}
