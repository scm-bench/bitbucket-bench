package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// The demo exists for the reader who has not configured anything yet, so it
// must work from a bare binary: no instance, no checkout, no flags beyond the
// one. It evaluates the same sample TestBundledSnapshotStillEvaluates covers
// from disk — this test is about the embedded copy reaching the same result.
func TestDemoEvaluatesTheBundledExample(t *testing.T) {
	stdout, stderr, code := run(t, "scan", "--demo", "-o", "json", "--fail-on", "none")

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d\n%s", code, ExitOK, stderr)
	}

	var rep struct {
		Metadata struct {
			Platform string `json:"platform"`
		} `json:"metadata"`
		Findings []struct {
			Status string `json:"status"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if rep.Metadata.Platform == "" {
		t.Error("the embedded sample does not record its platform")
	}
	if len(rep.Findings) == 0 {
		t.Fatal("the demo produced no findings, so it demonstrates nothing")
	}

	// The stderr line is what stays on the terminal when stdout is redirected.
	if !strings.Contains(stderr, "bundled example") {
		t.Errorf("stderr never says the scan was a demo:\n%s", stderr)
	}
}

// The table report must say it is example data where the eye lands first. The
// header names bitbucket.example.com, but nothing else would say the tool made
// that instance up.
func TestDemoTableIsMarkedAsExampleData(t *testing.T) {
	stdout, _, _ := run(t, "scan", "--demo", "--fail-on", "none")

	if !strings.Contains(stdout, "EXAMPLE DATA") {
		t.Errorf("table output carries no example-data banner:\n%s", stdout)
	}
}

// A real report must never carry the banner: it is the one line that tells a
// reader to discount everything below it.
func TestRealScanCarriesNoExampleBanner(t *testing.T) {
	fixture := writeSnapshotFixture(t)
	stdout, _, _ := run(t, "scan", "--snapshot-in", fixture, "--fail-on", "none")

	if strings.Contains(stdout, "EXAMPLE DATA") {
		t.Error("a snapshot scan was marked as example data")
	}
}

// The demo demonstrates the whole contract, exit code included: the sample
// carries HIGH failures, so under the default --fail-on high it exits 1
// exactly as a real scan of that instance would.
func TestDemoHonoursTheExitCodeContract(t *testing.T) {
	if _, _, code := run(t, "scan", "--demo"); code != ExitFindings {
		t.Errorf("exit code = %d, want %d for the sample's HIGH failures", code, ExitFindings)
	}
}

// A typed flag the demo would silently ignore is refused — the same rule
// --snapshot-in already applies to --project. Values merely inherited from the
// environment are not typed, and must not make the demo argue.
func TestDemoRejectsFlagsItWouldIgnore(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"url", []string{"scan", "--demo", "--url", "https://example.invalid"}},
		{"token", []string{"scan", "--demo", "--token", "t"}},
		{"project", []string{"scan", "--demo", "-p", "PRJ"}},
		{"repository", []string{"scan", "--demo", "-r", "PRJ/app"}},
		{"snapshot-in", []string{"scan", "--demo", "--snapshot-in", "x.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, code := run(t, tc.args...); code != ExitError {
				t.Errorf("exit code = %d, want %d", code, ExitError)
			}
		})
	}
}

func TestDemoIgnoresInheritedCredentials(t *testing.T) {
	t.Setenv("BITBUCKET_URL", "https://stale.example.com")
	t.Setenv("BITBUCKET_TOKEN", "stale")

	stdout, _, code := run(t, "scan", "--demo", "-o", "json", "--fail-on", "none")
	if code != ExitOK {
		t.Fatalf("exit code = %d; an exported BITBUCKET_URL must not break the demo", code)
	}
	if strings.Contains(stdout, "stale.example.com") {
		t.Error("the environment's URL leaked into the demo report")
	}
}

// The error a pipeline gets with nothing configured has to list every way
// out, the demo included: it is the reader's first contact with the tool, and
// "a flag is required" teaches less than showing the three exits.
func TestMissingInstanceErrorListsEveryWayOut(t *testing.T) {
	t.Setenv("BITBUCKET_URL", "")

	root := NewRootCommand()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"scan"})

	err := root.Execute()
	if err == nil {
		t.Fatal("scan with nothing configured succeeded")
	}
	for _, want := range []string{"--url", "BITBUCKET_URL", "--demo", "--snapshot-in"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error never mentions %s:\n%s", want, err)
		}
	}
}

// Enter alone runs the demo. The reader this menu exists for is the one with
// nothing to type, and the default has to serve exactly them.
func TestFirstRunPromptDefaultsToTheDemo(t *testing.T) {
	var out bytes.Buffer
	res, err := promptFirstRun(strings.NewReader("\n"), &out, false)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if !res.demo {
		t.Error("Enter did not choose the demo")
	}
	// The menu must show all three exits before asking.
	for _, want := range []string{"1.", "2.", "3.", "no instance configured"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("menu is missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "\033[") {
		t.Error("ANSI escapes with colour disabled")
	}
}

func TestFirstRunPromptCollectsCredentials(t *testing.T) {
	var out bytes.Buffer
	res, err := promptFirstRun(
		strings.NewReader("1\nhttps://bitbucket.example.com\nsekrit\n"), &out, false)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if res.demo {
		t.Error("choice 1 landed on the demo")
	}
	if res.url != "https://bitbucket.example.com" {
		t.Errorf("url = %q", res.url)
	}
	if res.token != "sekrit" {
		t.Errorf("token = %q", res.token)
	}
}

// An empty URL after choosing to enter one is the reader changing their mind,
// not a scan of nowhere.
func TestFirstRunPromptRejectsAnEmptyURL(t *testing.T) {
	var out bytes.Buffer
	if _, err := promptFirstRun(strings.NewReader("1\n\n"), &out, false); err == nil {
		t.Error("an empty URL was accepted")
	}
}

// Quitting, by choice or by EOF, gets the same guided error the
// non-interactive path prints — the menu must not know an exit the pipeline
// does not.
func TestFirstRunPromptQuitPathsCarryTheGuidedError(t *testing.T) {
	for name, input := range map[string]string{
		"choice 3":      "3\n",
		"eof":           "",
		"three retries": "x\ny\nz\n",
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			_, err := promptFirstRun(strings.NewReader(input), &out, false)
			if err == nil {
				t.Fatal("quit did not error")
			}
			if !strings.Contains(err.Error(), "--demo") {
				t.Errorf("quit error does not guide: %v", err)
			}
		})
	}
}

// readLine feeds a prompt that may hand the very next read to
// term.ReadPassword on the raw descriptor, so it must consume exactly one
// line — buffering past the newline would be input silently lost.
func TestReadLineConsumesExactlyOneLine(t *testing.T) {
	in := strings.NewReader("first\r\nsecond\n")

	got, err := readLine(in)
	if err != nil || got != "first" {
		t.Fatalf("readLine = %q, %v", got, err)
	}
	rest, _ := io.ReadAll(in)
	if string(rest) != "second\n" {
		t.Errorf("readLine read ahead; %q was left", rest)
	}
}
