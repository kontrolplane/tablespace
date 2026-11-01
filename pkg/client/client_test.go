package client

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// clearEnv unsets the variables libpq reads, for the duration of the test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PGHOST", "PGPORT", "PGUSER", "PGDATABASE", "PGPASSWORD", "PGSERVICE", "PGSERVICEFILE", "PGSSLMODE", "PGPASSFILE", "PGAPPNAME", "PGCONNECT_TIMEOUT"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestFlagsActAsEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("PGHOST", "from-env")
	t.Setenv("PGUSER", "env-user")
	cfg, err := Resolve(Options{Host: "from-flag", Port: "6543", DBName: "shop", SSLMode: "disable"})
	if err != nil {
		t.Fatal(err)
	}
	cc := cfg.ConnConfig
	if cc.Host != "from-flag" || cc.Port != 6543 || cc.User != "env-user" || cc.Database != "shop" || cc.TLSConfig != nil {
		t.Errorf("unexpected config host %q port %d user %q database %q", cc.Host, cc.Port, cc.User, cc.Database)
	}
}

func TestConnStringWinsOverFlags(t *testing.T) {
	clearEnv(t)
	cfg, err := Resolve(Options{Host: "from-flag", User: "flag-user", DBName: "postgres://url-user@url-host:5433/urldb?sslmode=disable"})
	if err != nil {
		t.Fatal(err)
	}
	cc := cfg.ConnConfig
	if cc.Host != "url-host" || cc.Port != 5433 || cc.User != "url-user" || cc.Database != "urldb" {
		t.Errorf("expected the url to win, got host %q port %d user %q database %q", cc.Host, cc.Port, cc.User, cc.Database)
	}

	cfg, err = Resolve(Options{User: "flag-user", DBName: "host=kv-host dbname=kvdb sslmode=disable"})
	if err != nil {
		t.Fatal(err)
	}
	if cc := cfg.ConnConfig; cc.Host != "kv-host" || cc.Database != "kvdb" || cc.User != "flag-user" {
		t.Errorf("expected the key/value string with the flag filling in, got host %q database %q user %q", cc.Host, cc.Database, cc.User)
	}
}

func TestServiceAndPassFile(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	services := filepath.Join(dir, "pg_service.conf")
	if err := os.WriteFile(services, []byte("[production]\nhost=prod-host\nport=5555\ndbname=proddb\nuser=prod-user\nsslmode=disable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pass := filepath.Join(dir, "pgpass")
	if err := os.WriteFile(pass, []byte("prod-host:5555:proddb:prod-user:s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSERVICEFILE", services)
	t.Setenv("PGPASSFILE", pass)
	cfg, err := Resolve(Options{Service: "production", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	cc := cfg.ConnConfig
	if cc.Host != "prod-host" || cc.Port != 5555 || cc.Database != "proddb" || cc.Password != "s3cret" {
		t.Errorf("expected the service with its password, got host %q port %d database %q", cc.Host, cc.Port, cc.Database)
	}
	if cc.RuntimeParams["default_transaction_read_only"] != "on" {
		t.Error("expected read only transactions")
	}
	if info := InfoOf(cfg, true); info.Host != "prod-host:5555" || !info.ReadOnly || info.User != "prod-user" {
		t.Errorf("unexpected info %+v", info)
	}
}

func TestResolveErrorsHidePasswords(t *testing.T) {
	clearEnv(t)
	_, err := Resolve(Options{DBName: "postgres://app:hunter2@host:notaport/db"})
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("expected an error without the password, got %v", err)
	}
}

func TestRedact(t *testing.T) {
	tests := map[string]string{
		"postgres://app:hunter2@host/db":                    "postgres://app:xxxxx@host/db",
		"failed: postgresql://a:b@h1,h2:5432/x?sslmode=off": "failed: postgresql://a:xxxxx@h1,h2:5432/x?sslmode=off",
		"postgres://app@host/db":                            "postgres://app@host/db",
		"host=h password=hunter2 user=u":                    "host=h password=xxxxx user=u",
		`host=h password='a b\' c' user=u`:                  "host=h password=xxxxx user=u",
		"cannot parse `postgres://u:p@x/y`: invalid port":   "cannot parse `postgres://u:xxxxx@x/y`: invalid port",
		"no secrets here":                                   "no secrets here",
	}
	for in, want := range tests {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsConnString(t *testing.T) {
	for s, want := range map[string]bool{"shop": false, "postgres://x": true, "postgresql://x": true, "host=x": true, "": false} {
		if got := IsConnString(s); got != want {
			t.Errorf("IsConnString(%q) = %v", s, got)
		}
	}
}

func TestDescribeUnreachable(t *testing.T) {
	clearEnv(t)
	cfg, err := Resolve(Options{DBName: "postgres://u:hunter2@127.0.0.1:1/db?connect_timeout=2"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Connect(context.Background(), cfg, "test", false)
	if err == nil {
		t.Fatal("expected the connection to fail")
	}
	msg := Describe(err, "tablespace")
	if !strings.Contains(msg, "is the server running?") || strings.Contains(msg, "hunter2") {
		t.Errorf("unexpected %q", msg)
	}
}

func TestDescribeServerRefusal(t *testing.T) {
	err := fmt.Errorf("failed to connect: tls error: refused; %w", &pgconn.PgError{Severity: "FATAL", Code: "28P01", Message: `password authentication failed for user "app"`})
	msg := Describe(err, "tablespace")
	if !strings.HasPrefix(msg, `error connecting to postgres: password authentication failed for user "app"`+"\n") || strings.Contains(msg, "tls") || !strings.Contains(msg, "PGPASSWORD") {
		t.Errorf("unexpected %q", msg)
	}
	msg = Describe(&pgconn.PgError{Code: "3D000", Message: `database "shop" does not exist`}, "tablespace")
	if !strings.Contains(msg, "--dbname") {
		t.Errorf("expected a hint at --dbname, got %q", msg)
	}
}
