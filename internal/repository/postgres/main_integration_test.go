//go:build integration

package postgres

import (
	"os"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Cleanup()
	os.Exit(code)
}
