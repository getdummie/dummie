package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/clickhouse" // registers scheme "clickhouse"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"     // registers scheme "pgx5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/urfave/cli/v3"
)

// The migrations travel inside the binary, so `control migrate up` is the same
// command wherever it runs and from whatever directory.
//
//go:embed migrations migrations-clickhouse
var migrationsFS embed.FS

// migrationSet is one database's migrations: its own embedded directory, its own DSN, and
// its own schema_migrations table. The two are versioned independently because
// they hold unrelated things -- postgres the control plane's own state,
// clickhouse the event stream shipped off the qemu hosts -- and a single version
// counter over both would make either one's history a lie.
type migrationSet struct {
	command string
	name    string
	dir     string
	env     string
	// dsn adapts what the operator wrote to what the migrate driver expects.
	dsn func(*url.URL)
}

var postgresMigrations = migrationSet{
	command: "migrate",
	name:    "postgres",
	dir:     "migrations",
	env:     "DATABASE_URL",
	// The pgx/v5 migrate driver registers the scheme "pgx5".
	dsn: func(u *url.URL) { u.Scheme = "pgx5" },
}

var clickhouseMigrations = migrationSet{
	command: "migrate-clickhouse",
	name:    "clickhouse",
	dir:     "migrations-clickhouse",
	env:     "CLICKHOUSE_URL",
	// Without this the driver hands the whole file to the server as one
	// statement, which fails on any migration that is more than a single DDL.
	dsn: func(u *url.URL) {
		q := u.Query()
		q.Set("x-multi-statement", "true")
		u.RawQuery = q.Encode()
	},
}

func migrateCommand(set migrationSet) *cli.Command {
	return &cli.Command{
		Name:  set.command,
		Usage: "apply or revert " + set.name + " migrations",
		Commands: []*cli.Command{
			// The argument is read with StringArg, not Args().First(): a declared
			// cli.Argument is consumed during parsing, so Args() is empty by the time
			// the action runs and every target silently became "all".
			{
				Name:      "up",
				Usage:     "apply migrations (optionally up to a target version, e.g. `up 10`)",
				Arguments: []cli.Argument{&cli.StringArg{Name: "target"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return runMigrate(set, true, cmd.StringArg("target"))
				},
			},
			{
				Name:      "down",
				Usage:     "revert migrations (optionally down to a target version, e.g. `down 10`)",
				Arguments: []cli.Argument{&cli.StringArg{Name: "target"}},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return runMigrate(set, false, cmd.StringArg("target"))
				},
			},
		},
	}
}

// migrateAllUp applies every migration in both sets. serve calls this in prod
// only: the image carries its migrations, so a deploy that came up is a deploy
// that has migrated. In dev they stay manual -- running `up` on whatever a
// branch has half-written is not a favour.
//
// A set whose DSN is unset is skipped, matching the rest of startup: the
// healthcheck already reports a missing database rather than refusing to boot.
func migrateAllUp() error {
	for _, set := range []migrationSet{postgresMigrations, clickhouseMigrations} {
		if os.Getenv(set.env) == "" {
			log.Printf("%s is not set; skipping %s migrations", set.env, set.name)
			continue
		}
		log.Printf("applying %s migrations", set.name)
		if err := runMigrate(set, true, ""); err != nil {
			return fmt.Errorf("%s migrations: %w", set.name, err)
		}
	}
	return nil
}

func newMigrator(set migrationSet) (*migrate.Migrate, error) {
	dsn := os.Getenv(set.env)
	if dsn == "" {
		return nil, errors.New(set.env + " is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", set.env, err)
	}
	set.dsn(u)
	src, err := iofs.New(migrationsFS, set.dir)
	if err != nil {
		return nil, err
	}
	return migrate.NewWithSourceInstance("iofs", src, u.String())
}

// versionsIn returns the sorted, de-duplicated migration versions in the set.
func versionsIn(dir string) ([]uint, error) {
	entries, err := fs.ReadDir(migrationsFS, dir)
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
func runMigrate(set migrationSet, up bool, targetArg string) error {
	m, err := newMigrator(set)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()

	versions, err := versionsIn(set.dir)
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
		return fmt.Errorf("%s is dirty at version %04d; resolve manually", set.name, cur)
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
			return fmt.Errorf("no migration %04d in %s/", target, set.dir)
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
		return reportVersion(m, set, ran, "applied")
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
	return reportVersion(m, set, ran, "reverted")
}

// reportVersion prints where the database ended up. Worth the extra query: the
// per-migration lines say what was attempted, not what the schema_migrations
// table now says, and those differ if a step stopped early.
func reportVersion(m *migrate.Migrate, set migrationSet, ran int, verb string) error {
	v, _, err := m.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		fmt.Printf("%d %s; %s is now at no version\n", ran, verb, set.name)
	case err != nil:
		return err
	default:
		fmt.Printf("%d %s; %s is now at version %04d\n", ran, verb, set.name, v)
	}
	return nil
}
