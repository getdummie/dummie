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
	_ "github.com/golang-migrate/migrate/v4/database/clickhouse"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/urfave/cli/v3"
)

//go:embed migrations migrations-clickhouse
var migrationsFS embed.FS

type migrationSet struct {
	command string
	name    string
	dir     string
	env     string
	dsn func(*url.URL)
}

var postgresMigrations = migrationSet{
	command: "migrate",
	name:    "postgres",
	dir:     "migrations",
	env:     "DATABASE_URL",
	dsn: func(u *url.URL) { u.Scheme = "pgx5" },
}

var clickhouseMigrations = migrationSet{
	command: "migrate-clickhouse",
	name:    "clickhouse",
	dir:     "migrations-clickhouse",
	env:     "CLICKHOUSE_URL",
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

	cur, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		cur = 0
	} else if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("%s is dirty at version %04d; resolve manually", set.name, cur)
	}

	var target uint
	if targetArg != "" {
		n, perr := strconv.ParseUint(targetArg, 10, 64)
		if perr != nil {
			return fmt.Errorf("invalid target version %q: %w", targetArg, perr)
		}
		target = uint(n)
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
