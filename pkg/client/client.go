// Package client connects to PostgreSQL the way psql does: a connection string or url, the PG*
// environment variables, the password file and the connection service file.
package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options holds what the flags set. A flag acts as the environment variable of the same name
// would, so a connection string or url passed as the database still wins, as it does with psql.
type Options struct {
	DBName   string // a database name, or a connection string or url
	Host     string
	Port     string
	User     string
	Service  string
	SSLMode  string
	ReadOnly bool // every transaction starts read only
}

// Info holds connection information for display.
type Info struct {
	Host     string // host:port, or the socket directory
	User     string
	Database string
	ReadOnly bool
}

// IsConnString reports whether s is a connection string or url rather than a database name, by
// the rule libpq uses: it starts with a postgres scheme or holds an equals sign.
func IsConnString(s string) bool {
	return strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://") || strings.Contains(s, "=")
}

// envFor pairs each flag with the environment variable it stands in for.
func (o Options) envFor() [][2]string {
	pairs := [][2]string{
		{"PGHOST", o.Host},
		{"PGPORT", o.Port},
		{"PGUSER", o.User},
		{"PGSERVICE", o.Service},
		{"PGSSLMODE", o.SSLMode},
	}
	if !IsConnString(o.DBName) {
		pairs = append(pairs, [2]string{"PGDATABASE", o.DBName})
	}
	return pairs
}

// Resolve turns the options into a pool configuration. The flags that are set take the place of
// their environment variables, then the connection string, if any, is parsed on top of them.
func Resolve(o Options) (*pgxpool.Config, error) {
	for _, kv := range o.envFor() {
		if kv[1] != "" {
			if err := os.Setenv(kv[0], kv[1]); err != nil {
				return nil, err
			}
		}
	}
	connString := ""
	if IsConnString(o.DBName) {
		connString = o.DBName
	}
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, errors.New(Redact(err.Error()))
	}
	cc := cfg.ConnConfig
	if o.ReadOnly {
		cc.RuntimeParams["default_transaction_read_only"] = "on"
	}
	// A cancelled query is cancelled on the server too, rather than the connection dropped.
	cc.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, CancelRequestDelay: 0, DeadlineDelay: 2 * time.Second}
	}
	cfg.MaxConns = 4
	cfg.MinConns = 0
	cfg.MaxConnIdleTime = time.Minute
	return cfg, nil
}

// NeedsPassword reports whether err is the server asking for a password that was not given.
func NeedsPassword(cfg *pgxpool.Config, err error) bool {
	var pgErr *pgconn.PgError
	return cfg.ConnConfig.Password == "" && errors.As(err, &pgErr) && pgErr.Code == "28P01"
}

// Connect opens a pool for cfg and checks it reaches the server.
func Connect(ctx context.Context, cfg *pgxpool.Config, name string, readOnly bool) (*pgxpool.Pool, Info, error) {
	if _, ok := cfg.ConnConfig.RuntimeParams["application_name"]; !ok {
		cfg.ConnConfig.RuntimeParams["application_name"] = name
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, Info{}, errors.New(Redact(err.Error()))
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, Info{}, err
	}
	return pool, InfoOf(cfg, readOnly), nil
}

// InfoOf describes the connection cfg makes.
func InfoOf(cfg *pgxpool.Config, readOnly bool) Info {
	cc := cfg.ConnConfig
	host := cc.Host
	if !strings.HasPrefix(host, "/") {
		host = net.JoinHostPort(host, strconv.Itoa(int(cc.Port)))
	}
	return Info{Host: host, User: cc.User, Database: cc.Database, ReadOnly: readOnly}
}

// WithDatabase returns a copy of cfg that connects to database instead.
func WithDatabase(cfg *pgxpool.Config, database string) *pgxpool.Config {
	c := cfg.Copy()
	c.ConnConfig.Database = database
	return c
}

// Redact hides the passwords in the postgres urls and password settings within s.
func Redact(s string) string {
	var b strings.Builder
	for {
		i := indexScheme(s)
		if i < 0 {
			break
		}
		b.WriteString(s[:i])
		s = s[i:]
		end := strings.IndexAny(s, " \t\n`'\"")
		if end < 0 {
			end = len(s)
		}
		b.WriteString(redactURL(s[:end]))
		s = s[end:]
	}
	b.WriteString(s)
	return redactPasswordSetting(b.String())
}

func indexScheme(s string) int {
	i := strings.Index(s, "postgres://")
	if j := strings.Index(s, "postgresql://"); j >= 0 && (i < 0 || j < i) {
		i = j
	}
	return i
}

// redactURL hides the password in a url's userinfo.
func redactURL(u string) string {
	scheme, rest, _ := strings.Cut(u, "://")
	authority := rest
	if j := strings.IndexAny(rest, "/?"); j >= 0 {
		authority = rest[:j]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return u
	}
	user, _, ok := strings.Cut(authority[:at], ":")
	if !ok {
		return u
	}
	return scheme + "://" + user + ":xxxxx" + rest[at:]
}

// redactPasswordSetting hides the value of password= in a key/value connection string.
func redactPasswordSetting(s string) string {
	const key = "password="
	var b strings.Builder
	for {
		i := strings.Index(s, key)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+len(key)])
		s = s[i+len(key):]
		end := 0
		if strings.HasPrefix(s, "'") {
			end = 1
			for end < len(s) && s[end] != '\'' {
				if s[end] == '\\' {
					end++
				}
				end++
			}
			end = min(end+1, len(s))
		} else {
			for end < len(s) && s[end] != ' ' {
				end++
			}
		}
		b.WriteString("xxxxx")
		s = s[end:]
	}
}

// Describe words a connection error for the terminal, with a hint when the server could not be
// reached at all.
func Describe(err error, program string) string {
	msg := "error connecting to postgres: " + Redact(err.Error())
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) && isUnreachable(err) {
		msg += fmt.Sprintf("\nis the server running? pick one with --host, --dbname or PGHOST (see %s --help)", program)
	}
	return msg
}

func isUnreachable(err error) bool {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	return errors.As(err, &opErr) || errors.As(err, &dnsErr) || errors.Is(err, context.DeadlineExceeded)
}
