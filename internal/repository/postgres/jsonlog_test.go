package postgres

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// captureLog redirects the standard logger for the duration of fn and returns
// what was written to it.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

func TestUnmarshalJSONB_LogsMalformedColumn(t *testing.T) {
	var dest map[string]any
	out := captureLog(t, func() {
		unmarshalJSONB([]byte(`{"broken`), &dest, "components", "comp-7", "extra")
	})

	for _, want := range []string{"components", "comp-7", "extra"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q does not mention %q", out, want)
		}
	}
}

func TestUnmarshalJSONB_SilentOnValidColumn(t *testing.T) {
	var dest map[string]any
	out := captureLog(t, func() {
		unmarshalJSONB([]byte(`{"a":1}`), &dest, "components", "comp-7", "extra")
	})

	if out != "" {
		t.Errorf("expected no log for a valid column, got %q", out)
	}
	if dest["a"] != float64(1) {
		t.Errorf("expected the column to be decoded, got %#v", dest)
	}
}

// An absent column is a value that is not there, not a value that failed to
// decode — logging it would drown the real failures in noise.
func TestUnmarshalJSONB_SilentOnEmptyColumn(t *testing.T) {
	var dest map[string]any
	out := captureLog(t, func() {
		unmarshalJSONB(nil, &dest, "components", "comp-7", "extra")
	})

	if out != "" {
		t.Errorf("expected no log for a NULL column, got %q", out)
	}
}

// fakeRow implements scanner, feeding scanComponent a fixed set of column
// values so a malformed `extra` can be exercised without a database.
type fakeRow struct {
	id       string
	extraRaw []byte
}

func (f fakeRow) Scan(dest ...any) error {
	strs := []string{f.id, "repo-id", "repo-name", "npm", "", "lodash", "1.0.0"}
	next := 0
	for _, d := range dest {
		switch v := d.(type) {
		case *string:
			if next < len(strs) {
				*v = strs[next]
				next++
			}
		case *[]string:
			*v = []string{}
		case *[]byte:
			*v = f.extraRaw
		case **time.Time:
			*v = nil
		case *int64:
			*v = 0
		case *time.Time:
			*v = time.Time{}
		}
	}
	return nil
}

// The call site, not just the helper: a malformed column has to be reported by
// the code that actually reads one.
func TestScanComponent_LogsMalformedExtra(t *testing.T) {
	var c *domain.Component
	var err error
	out := captureLog(t, func() {
		c, err = scanComponent(fakeRow{id: "comp-42", extraRaw: []byte(`{"scan_result":`)})
	})

	if err != nil {
		t.Fatalf("a malformed extra must not fail the read: %v", err)
	}
	if c.Name != "lodash" {
		t.Errorf("expected the rest of the row to survive, got %#v", c)
	}
	if !strings.Contains(out, "comp-42") || !strings.Contains(out, "extra") {
		t.Errorf("log %q does not identify the row and column", out)
	}
}
