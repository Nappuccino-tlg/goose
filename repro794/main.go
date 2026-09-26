// Reproduction for pressly/goose#794, where CREATE INDEX CONCURRENTLY fails with
// SQLSTATE 25001 inside a migration registered with AddMigrationNoTx.
//
// Both cases below run through goose as a RunDB (no-transaction) Go migration, so
// whatever goose does to the connection is held constant. The only thing that differs
// is whether the two statements are sent in one Exec or in two.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// The partitioned table from the PostgreSQL docs, which is what the issue was following.
const setup = `
CREATE TABLE measurement (city_id int not null, logdate date not null, unitsales int)
    PARTITION BY RANGE (logdate);
CREATE TABLE measurement_y2006m02 PARTITION OF measurement
    FOR VALUES FROM ('2006-02-01') TO ('2006-03-01');
CREATE INDEX measurement_usls_idx ON ONLY measurement (unitsales);
`

// Exactly as the issue wrote it: two statements, one Exec.
const combined = `
CREATE INDEX CONCURRENTLY measurement_usls_200602_idx
ON measurement_y2006m02 (unitsales);
ALTER INDEX measurement_usls_idx
ATTACH PARTITION measurement_usls_200602_idx;
`

// The same two statements, one Exec each.
var split = []string{
	`CREATE INDEX CONCURRENTLY measurement_usls_200602_idx ON measurement_y2006m02 (unitsales)`,
	`ALTER INDEX measurement_usls_idx ATTACH PARTITION measurement_usls_200602_idx`,
}

func oneExec(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, combined)
	return err
}

func twoExecs(ctx context.Context, db *sql.DB) error {
	for _, stmt := range split {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	ctx := context.Background()
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		fail(err)
	}
	defer db.Close()

	failures := 0
	for _, c := range []struct {
		name string
		fn   func(context.Context, *sql.DB) error
	}{
		{"one Exec, two statements", oneExec},
		{"two Execs, one statement each", twoExecs},
	} {
		reset(ctx, db)

		// RunDB is what AddMigrationNoTx sets, so goose hands the function a *sql.DB
		// and opens no transaction of its own.
		m := goose.NewGoMigration(1, &goose.GoFunc{RunDB: c.fn}, nil)
		p, err := goose.NewProvider(goose.DialectPostgres, db, nil, goose.WithGoMigrations(m))
		if err != nil {
			fail(err)
		}
		if _, err := p.Up(ctx); err != nil {
			fmt.Printf("%-32s FAILED  %v\n", c.name, err)
			failures++
			continue
		}
		fmt.Printf("%-32s ok\n", c.name)
	}

	// The SQL migration the README change proposes, run verbatim: up, then down.
	if err := sqlExample(ctx, db); err != nil {
		fmt.Printf("%-32s FAILED  %v\n", "README sql example", err)
		failures++
	} else {
		fmt.Printf("%-32s ok\n", "README sql example")
	}

	fmt.Printf("\ngoose %s\n", goose.VERSION)
	if failures != 1 {
		fmt.Printf("expected exactly one failure (the one Exec case), got %d\n", failures)
		os.Exit(1)
	}
}

func sqlExample(ctx context.Context, db *sql.DB) error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS goose_db_version`,
		`DROP TABLE IF EXISTS users`,
		`CREATE TABLE users (id int, email text)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("repro794/migrations"))
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("up: %w", err)
	}
	if _, err := p.Down(ctx); err != nil {
		return fmt.Errorf("down: %w", err)
	}
	return nil
}

func reset(ctx context.Context, db *sql.DB) {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS goose_db_version`,
		`DROP TABLE IF EXISTS measurement CASCADE`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			fail(err)
		}
	}
	if _, err := db.ExecContext(ctx, setup); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "setup failed:", err)
	os.Exit(1)
}
