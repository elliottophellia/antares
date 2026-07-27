package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/enowdev/antares/internal/backup"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/version"
)

func cmdBackup(args []string) error {
	verb := "create"
	if len(args) > 0 {
		verb, args = args[0], args[1:]
	}

	dir := config.Path("backups")

	switch verb {
	case "create", "now":
		extra, note := databaseFiles()
		info, err := backup.Create(config.Home(), dir, version.Version, extra, quiesceDatabase)
		if err != nil {
			return err
		}
		fmt.Printf("Wrote %s (%s)\n", info.Path, humanBytes(info.Size))
		if note != "" {
			fmt.Println(note)
		}
		return nil

	case "list", "ls":
		list, err := backup.List(dir)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No backups yet. Make one with `antares backup`.")
			return nil
		}
		for _, b := range list {
			fmt.Printf("%-40s %10s  %s\n", b.Name, humanBytes(b.Size),
				b.CreatedAt.Format("2 Jan 2006 15:04"))
		}
		return nil

	case "restore":
		if len(args) == 0 {
			return errors.New("usage: antares backup restore <file> [--force]")
		}
		path := args[0]
		force := false
		for _, a := range args[1:] {
			if a == "--force" || a == "-f" {
				force = true
			}
		}
		// A path with no separator is taken as a name in the backups directory,
		// so `restore antares-2026-01-01-120000.tar.gz` works.
		if !strings.ContainsRune(path, os.PathSeparator) {
			path = config.Path("backups", path)
		}
		n, err := backup.Restore(path, config.Home(), force)
		if err != nil {
			return err
		}
		fmt.Printf("Restored %d file(s) into %s\n", n, config.Home())
		fmt.Println("Start Antares again to pick it up.")
		return nil

	case "prune":
		keep := 5
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
				keep = n
			}
		}
		removed, err := backup.Prune(dir, keep)
		if err != nil {
			return err
		}
		fmt.Printf("Removed %d, kept the newest %d\n", removed, keep)
		return nil
	}
	return fmt.Errorf("unknown backup command %q — try create, list, restore, or prune", verb)
}

// quiesceDatabase folds the write-ahead log back into the database file, so a
// copy of that file is a complete database rather than half of one.
func quiesceDatabase() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Database.Driver != "sqlite" {
		// Postgres is not in the state directory at all; backing it up is
		// pg_dump's job, not ours.
		return nil
	}
	db, err := store.Open(context.Background(), cfg.Database.Driver, cfg.Database.DSN,
		cfg.Database.MaxConns, cfg.Database.Busy, cfg.Database.WAL)
	if err != nil {
		// A database that will not open has nothing to flush.
		return nil
	}
	defer db.Close()
	return db.Ping(context.Background())
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// databaseFiles reports the SQLite file to include, and a line explaining what
// a backup does and does not cover. A backup that quietly omits the database
// is worse than none, because it looks like one.
func databaseFiles() ([]string, string) {
	cfg, err := config.Load()
	if err != nil {
		return nil, ""
	}
	switch cfg.Database.Driver {
	case "sqlite":
		path := config.Expand(cfg.Database.DSN)
		// A DSN can carry query parameters; the file is the part before them.
		if i := strings.IndexAny(path, "?"); i >= 0 {
			path = path[:i]
		}
		if path == "" {
			return nil, ""
		}
		return []string{path}, "Includes the database at " + path
	case "postgres":
		return nil, "The database is on Postgres and is not in this archive — use pg_dump for that."
	}
	return nil, ""
}
