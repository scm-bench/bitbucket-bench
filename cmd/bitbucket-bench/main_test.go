package main

import (
	"os"
	"testing"

	"github.com/scm-bench/bitbucket-bench/internal/cli"
)

// main wires the command tree to the process: its arguments in, its exit code
// out.
func TestMainExitsWithTheCommandsCode(t *testing.T) {
	origArgs, origExit := os.Args, exit
	t.Cleanup(func() { os.Args, exit = origArgs, origExit })

	for args, want := range map[string]int{
		"version":       cli.ExitOK,
		"no-such-thing": cli.ExitError,
	} {
		got := -1
		exit = func(code int) { got = code }
		os.Args = []string{"bitbucket-bench", args}
		main()
		if got != want {
			t.Errorf("bitbucket-bench %s exited %d, want %d", args, got, want)
		}
	}
}
