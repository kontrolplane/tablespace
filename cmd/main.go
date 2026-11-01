package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/kontrolplane/pgui/pkg/client"
	tui "github.com/kontrolplane/pgui/pkg/tui"
	"github.com/kontrolplane/pgui/pkg/tui/styles"
)

var (
	projectName = "kontrolplane"
	programName = "pgui"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// shorthands maps a flag to its one letter alias, listed together in the usage. They are the
// ones psql uses, but for -h, which is help here as it is everywhere else.
var shorthands = map[string]string{"dbname": "d", "port": "p", "user": "U"}

// usage prints the help, with the flags spelled with two dashes as the readme does.
func usage(w io.Writer, fs *flag.FlagSet) {
	_, _ = fmt.Fprintf(w, `%[1]s is a terminal user interface for managing PostgreSQL databases, tables, rows and sessions.

usage: %[1]s [flags]

it connects the way psql does: flags, then the PG* environment variables, ~/.pgpass and
pg_service.conf. a connection string or url passed as --dbname takes precedence over them.

examples:
  %[1]s
  %[1]s --host localhost --user postgres --dbname shop
  %[1]s --dbname postgres://postgres@localhost:5432/shop?sslmode=disable
  %[1]s --service production --read-only

flags:
`, programName)
	aliases := map[string]bool{}
	for _, short := range shorthands {
		aliases[short] = true
	}
	fs.VisitAll(func(f *flag.Flag) {
		if aliases[f.Name] {
			return
		}
		name := "--" + f.Name
		if short, ok := shorthands[f.Name]; ok {
			name = "-" + short + ", " + name
		}
		if kind, _ := flag.UnquoteUsage(f); kind != "" {
			name += " " + kind
		}
		line := "  " + name
		// Other defaults come from the environment, and may be secrets.
		if f.Name == "theme" {
			_, _ = fmt.Fprintf(w, "%-28s %s (default %q)\n", line, f.Usage, f.DefValue)
			return
		}
		_, _ = fmt.Fprintf(w, "%-28s %s\n", line, f.Usage)
	})
}

func Execute(version, commit, date string) {
	var opts client.Options

	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.DBName, "dbname", "", "database name, or a connection string or url (env PGDATABASE)")
	fs.StringVar(&opts.DBName, "d", "", "shorthand for --dbname")
	fs.StringVar(&opts.Host, "host", "", "server host or socket directory (env PGHOST)")
	fs.StringVar(&opts.Port, "port", "", "server port (env PGPORT)")
	fs.StringVar(&opts.Port, "p", "", "shorthand for --port")
	fs.StringVar(&opts.User, "user", "", "user name (env PGUSER)")
	fs.StringVar(&opts.User, "U", "", "shorthand for --user")
	fs.StringVar(&opts.Service, "service", "", "service from pg_service.conf (env PGSERVICE)")
	fs.StringVar(&opts.SSLMode, "sslmode", "", "disable, allow, prefer, require, verify-ca or verify-full (env PGSSLMODE)")
	fs.BoolVar(&opts.ReadOnly, "read-only", false, "start every transaction read only and refuse changes in the interface")
	theme := fs.String("theme", "auto", "colour theme: auto follows the terminal, dark or light also paint the background")
	debug := fs.Bool("debug", false, "write debug logs to debug.log")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(os.Stdout, fs)
			return
		}
		fmt.Fprintf(os.Stderr, "%s (see %s --help)\n", styles.Clean(err.Error()), programName)
		os.Exit(2)
	}

	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q, %s only takes flags, pick a database with --dbname (see %s --help)\n", fs.Arg(0), programName, programName)
		os.Exit(2)
	}

	if *showVersion {
		fmt.Printf("%s %s (commit %s, built %s)\n", programName, version, commit, date)
		return
	}

	switch *theme {
	case "dark":
		styles.Use(true)
		styles.Paint = true
	case "light":
		styles.Use(false)
		styles.Paint = true
	case "auto":
		styles.Use(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	default:
		fmt.Fprintf(os.Stderr, "unknown theme %q, use auto, dark or light\n", styles.Clean(*theme))
		os.Exit(2)
	}

	log.SetOutput(io.Discard)
	if *debug {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			fail("could not open debug.log: %s", styles.Clean(err.Error()))
		}
		defer func() { _ = f.Close() }()
	}

	cfg, err := client.Resolve(opts)
	if err != nil {
		fail("%s (see %s --help)", styles.Clean(err.Error()), programName)
	}
	pool, info, err := connect(cfg, opts.ReadOnly)
	if err != nil {
		fail("%s", styles.CleanBlock(client.Describe(err, programName)))
	}
	defer pool.Close()

	model := tui.NewModel(projectName, programName, pool, cfg, info)
	if _, err := tea.NewProgram(model).Run(); err != nil {
		pool.Close()
		fail("error running program: %s", styles.Clean(err.Error()))
	}
}

// connect opens the pool. When the server asks for a password none was given for, it is asked
// for once on the terminal, as psql does.
func connect(cfg *pgxpool.Config, readOnly bool) (*pgxpool.Pool, client.Info, error) {
	name := projectName + "/" + programName
	pool, info, err := client.Connect(context.Background(), cfg, name, readOnly)
	if err == nil || !client.NeedsPassword(cfg, err) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return pool, info, err
	}
	password, perr := readPassword(fmt.Sprintf("password for user %s: ", cfg.ConnConfig.User))
	if perr != nil {
		return nil, client.Info{}, err
	}
	cfg.ConnConfig.Password = password
	return client.Connect(context.Background(), cfg, name, readOnly)
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	defer fmt.Fprintln(os.Stderr)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}
