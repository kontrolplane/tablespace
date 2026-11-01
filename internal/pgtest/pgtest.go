// Package pgtest runs an embedded postgres for the tests of a package, and hands every test a
// database of its own.
package pgtest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	user     = "pgui"
	password = "pgui"
)

var (
	port    uint32
	created atomic.Int64
)

// Main starts the server, runs the tests of the package and stops it again. Call it from TestMain.
func Main(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "pgui-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	port, err = freePort()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var logs bytes.Buffer
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V18).
		Port(port).
		Username(user).
		Password(password).
		Database("postgres").
		RuntimePath(filepath.Join(dir, "runtime")).
		DataPath(filepath.Join(dir, "data")).
		StartTimeout(time.Minute).
		Logger(&logs))
	if err := pg.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "starting postgres: %v\n%s", err, logs.String())
		return 1
	}
	defer func() { _ = pg.Stop() }()
	return m.Run()
}

func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}

// URL is the connection url of database.
func URL(database string) string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", user, password, port, database)
}

// Config is the pool configuration for database.
func Config(t testing.TB, database string) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(URL(database))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	return cfg
}

// Database creates a database for the test, runs setup in it and returns a pool connected to it.
// The database is dropped once the test is done.
func Database(t testing.TB, setup ...string) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("t_%d", created.Add(1))

	admin, err := pgxpool.NewWithConfig(ctx, Config(t, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "create database "+name); err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, Config(t, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin, err := pgxpool.NewWithConfig(ctx, Config(t, "postgres"))
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.Exec(ctx, "drop database if exists "+name+" with (force)")
	})
	for _, sql := range setup {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("setup %q: %v", sql, err)
		}
	}
	return pool, name
}
