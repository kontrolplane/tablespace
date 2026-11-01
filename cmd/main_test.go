package cmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// run runs the program as the test binary, with args and the environment given.
func run(t *testing.T, args []string, env ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunProgram$")
	cmd.Env = append(os.Environ(), append([]string{"PGUI_RUN_ARGS=" + strings.Join(args, "\x1f")}, env...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// TestRunProgram runs the program when asked to by run, and does nothing otherwise.
func TestRunProgram(t *testing.T) {
	args, ok := os.LookupEnv("PGUI_RUN_ARGS")
	if !ok {
		return
	}
	os.Args = append([]string{programName}, strings.Split(args, "\x1f")...)
	if args == "" {
		os.Args = os.Args[:1]
	}
	Execute("dev", "none", "unknown")
}

func TestHelpGoesToStdout(t *testing.T) {
	out, errOut, code := run(t, []string{"--help"}, "PGPASSWORD=hunter2", "PGHOST=secret-host")
	if code != 0 {
		t.Fatalf("expected --help to exit 0, got %d: %s", code, errOut)
	}
	for _, want := range []string{"usage: pgui [flags]", "  -d, --dbname string", "  -U, --user string", "  --host string", "  --read-only", `(default "auto")`} {
		if !strings.Contains(out, want) {
			t.Errorf("expected the help on stdout to contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  -dbname") || strings.Contains(out, "\n  -d ") {
		t.Errorf("expected the flags with two dashes and the shorthand next to its flag:\n%s", out)
	}
	if strings.Contains(out, "hunter2") || strings.Contains(out, "secret-host") {
		t.Error("expected defaults from the environment to stay out of the help")
	}
	if errOut != "" {
		t.Errorf("expected nothing on stderr, got %q", errOut)
	}
}

func TestBadArgumentsExitTwo(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"shop"}, {"--theme", "pink"}} {
		_, errOut, code := run(t, args)
		if code != 2 || errOut == "" {
			t.Errorf("%v: expected exit 2 with a hint, got %d: %s", args, code, errOut)
		}
	}
}

func TestVersion(t *testing.T) {
	out, _, code := run(t, []string{"--version"})
	// The test binary adds its own PASS after the program returns.
	if code != 0 || !strings.HasPrefix(out, "pgui dev (commit none, built unknown)\n") {
		t.Errorf("unexpected version output %q (exit %d)", out, code)
	}
}

func TestUnreachableServerHintsAndRedacts(t *testing.T) {
	_, errOut, code := run(t, []string{"--dbname", "postgres://app:hunter2@127.0.0.1:1/shop?connect_timeout=2"})
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, errOut)
	}
	if strings.Contains(errOut, "hunter2") {
		t.Errorf("expected the password left out of the error:\n%s", errOut)
	}
	if !strings.Contains(errOut, "is the server running?") {
		t.Errorf("expected a hint on reaching the server:\n%s", errOut)
	}
}
