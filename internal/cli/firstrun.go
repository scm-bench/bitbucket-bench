package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/scm-bench/scm-bench/examples"
	"github.com/scm-bench/scm-bench/internal/console"
	"github.com/scm-bench/scm-bench/internal/scm"
)

// This file is what `scm-bench scan` does before it has an instance to scan.
//
// A pipeline with nothing configured gets an error that lists every way out —
// that is errNoInstance. A person at a terminal gets a menu instead, because
// the moment someone runs the tool bare is the one moment they demonstrably do
// not know the flags yet, and an error saying "go configure things" turns away
// exactly the reader who wanted to see what a report looks like.

// demoNotice marks the demo report as example data. It leads the table output,
// because the report's header names bitbucket.example.com and nothing else
// would say the tool made that instance up.
const demoNotice = "EXAMPLE DATA — this report describes the bundled sample instance, not yours. Pass --url to scan your own."

// demoSnapshot decodes the sample that is compiled into the binary, so the
// demo works for someone who installed a release archive and has no checkout.
func demoSnapshot() (*scm.Snapshot, error) {
	return parseSnapshot(examples.SnapshotJSON, "the bundled example")
}

// errNoInstance is the way out for every path that ends without an instance:
// the non-interactive error, and the menu's quit. It lists all three exits
// rather than naming one flag, because the reader who hits it has not chosen
// between them yet.
func errNoInstance() error {
	return errors.New(`no instance configured: pass --url, or set BITBUCKET_URL
  scan an instance:        scm-bench scan --url https://bitbucket.example.com --token "$BITBUCKET_TOKEN"
  see a sample report:     scm-bench scan --demo
  evaluate a saved file:   scm-bench scan --snapshot-in snapshot.json`)
}

// firstRunResult is what the menu settled on: the demo, or an instance.
type firstRunResult struct {
	demo  bool
	url   string
	token string
}

// promptFirstRun offers the choice on out and reads the answer from in. The
// caller has already established that both ends are terminals; this function
// still survives an in that is not one, because `scan < /dev/null` stats as a
// character device and must fall through to the error, not hang.
//
// Enter alone runs the demo. That default is the point of the menu: the reader
// with credentials to type has somewhere to type them, and the reader without
// any gets shown what the tool produces instead of being asked again for what
// they do not have.
func promptFirstRun(in io.Reader, out io.Writer, color bool) (firstRunResult, error) {
	p := console.Painter{Enabled: color}
	w := console.Writer{W: out, P: p}

	w.Line(console.Warn, "no instance configured — scan needs a Bitbucket URL and a credential")
	w.Blank()
	fmt.Fprintf(out, "  %s enter the URL and token now\n", p.Paint(console.Cyan, "1."))
	fmt.Fprintf(out, "  %s show a sample report from the bundled example\n", p.Paint(console.Cyan, "2."))
	fmt.Fprintf(out, "  %s quit\n", p.Paint(console.Cyan, "3."))
	w.Blank()

	// Three attempts, then the error. An unattended loop on a stdin that keeps
	// producing garbage would spin forever asking a question nobody is there
	// to answer.
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprintf(out, "choose [1/2/3] (Enter = 2): ")
		choice, err := readLine(in)
		if err != nil {
			// EOF: nobody is on the other end after all.
			fmt.Fprintln(out)
			return firstRunResult{}, errNoInstance()
		}
		switch strings.TrimSpace(choice) {
		case "1":
			return promptCredentials(in, out)
		case "", "2":
			return firstRunResult{demo: true}, nil
		case "3", "q":
			return firstRunResult{}, errNoInstance()
		}
	}
	return firstRunResult{}, errNoInstance()
}

func promptCredentials(in io.Reader, out io.Writer) (firstRunResult, error) {
	fmt.Fprintf(out, "Bitbucket URL (e.g. https://bitbucket.example.com): ")
	url, err := readLine(in)
	if err != nil {
		fmt.Fprintln(out)
		return firstRunResult{}, errNoInstance()
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return firstRunResult{}, errNoInstance()
	}

	fmt.Fprintf(out, "HTTP access token (hidden; Enter to scan anonymously): ")
	token, err := readSecret(in, out)
	if err != nil {
		fmt.Fprintln(out)
		return firstRunResult{}, errNoInstance()
	}
	return firstRunResult{url: url, token: strings.TrimSpace(token)}, nil
}

// readSecret reads the token without echoing it when in is a real terminal.
// This is what golang.org/x/term was finally added for: the two places that
// declined it before needed one boolean and one integer, but a credential
// echoed onto a terminal ends up in scrollback and screen recordings, and
// that is not a trade a security tool gets to make.
func readSecret(in io.Reader, out io.Writer) (string, error) {
	if f, ok := in.(*os.File); ok && isTerminal(f) {
		raw, err := term.ReadPassword(int(f.Fd()))
		// The Enter that submitted the secret was swallowed with it.
		fmt.Fprintln(out)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	}
	return readLine(in)
}

// readLine reads one line, one byte at a time. Deliberately unbuffered: a
// bufio.Reader can read ahead past the newline, and the very next read here
// may be term.ReadPassword going straight to the file descriptor — anything
// sitting in a buffer at that moment would be input the prompt silently lost.
func readLine(in io.Reader) (string, error) {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimRight(string(line), "\r"), nil
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return string(line), nil
			}
			return "", err
		}
	}
}
