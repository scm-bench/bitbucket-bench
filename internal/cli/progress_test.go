package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// A redirected stream has no cursor to move, so carriage returns would pile up
// as thousands of lines in a CI log. Nothing should be written at all.
func TestProgressIsSuppressedWhenNotATerminal(t *testing.T) {
	var buf bytes.Buffer
	if p := newProgressWriter(&buf, true); p != nil {
		t.Error("a non-terminal writer should get no progress")
	}
	// A pipe is the shape a redirect actually takes.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if p := newProgressWriter(w, true); p != nil {
		t.Error("a pipe should get no progress")
	}
}

func TestProgressDisabledReturnsNothing(t *testing.T) {
	if p := newProgressWriter(os.Stderr, false); p != nil {
		t.Error("progress was disabled but a writer was returned")
	}
}

// The nil writer is the normal case for a redirected run, so every method has
// to tolerate it rather than the caller having to check.
func TestNilProgressWriterIsInert(t *testing.T) {
	var p *progressWriter
	p.update("something")
	p.clear()
	if cb := p.callback(); cb != nil {
		t.Error("a nil writer should report no callback, so the fetcher can skip formatting")
	}
}

func TestProgressOverwritesInPlace(t *testing.T) {
	var buf bytes.Buffer
	p := &progressWriter{out: &buf}

	p.update("scanning 1/10 repositories")
	p.update("scanning 2/10")

	out := buf.String()
	if strings.Contains(out, "\n") {
		t.Errorf("progress must stay on one line, got %q", out)
	}
	if n := strings.Count(out, "\r"); n != 2 {
		t.Errorf("expected one carriage return per update, got %d in %q", n, out)
	}
	// The shorter second line has to blank the tail of the first, or "1/10
	// repositories" would leave "repositories" stranded on the line.
	if !strings.HasSuffix(out, strings.Repeat(" ", len("scanning 1/10 repositories")-len("scanning 2/10"))) {
		t.Errorf("a shorter update did not pad over the previous line: %q", out)
	}
}

func TestProgressClearLeavesTheLineBlank(t *testing.T) {
	var buf bytes.Buffer
	p := &progressWriter{out: &buf}

	p.update("scanning 1/10")
	buf.Reset()
	p.clear()

	out := buf.String()
	if !strings.HasPrefix(out, "\r") || !strings.HasSuffix(out, "\r") {
		t.Errorf("clear should return the cursor before and after blanking, got %q", out)
	}
	if strings.TrimSpace(strings.ReplaceAll(out, "\r", "")) != "" {
		t.Errorf("clear should write only blanks, got %q", out)
	}
	// Clearing twice must not blank a line that is already gone.
	buf.Reset()
	p.clear()
	if buf.Len() != 0 {
		t.Errorf("a second clear wrote %q", buf.String())
	}
}

// The fetcher calls update from every repository goroutine.
func TestProgressIsSafeForConcurrentUse(t *testing.T) {
	p := &progressWriter{out: &bytes.Buffer{}}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.update(strings.Repeat("x", i%20))
		}(i)
	}
	wg.Wait()
}

// A deadline the operator set should be named as the cause. The bare error is
// whichever request happened to be in flight, which reads as a network fault.
func TestScanFailureNamesTheDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	opts := &scanOptions{maxDuration: 5 * time.Minute}
	err := describeScanFailure(ctx, opts, context.DeadlineExceeded)
	if err == nil || !strings.Contains(err.Error(), "--max-duration 5m0s") {
		t.Errorf("error = %v, want it to name the deadline", err)
	}

	// Without a deadline set, the original error must pass through untouched:
	// blaming a limit nobody configured would be worse than saying nothing.
	noDeadline := &scanOptions{}
	original := context.DeadlineExceeded
	if got := describeScanFailure(ctx, noDeadline, original); got != original {
		t.Errorf("error = %v, want the original error unchanged", got)
	}
}
