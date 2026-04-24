package audit

import "time"

// SetNowFuncForTest replaces the rotator's clock for deterministic tests.
// Lives in a non-test file with `_test.go` suffix so it is compiled only
// during `go test` for this package, not exported into production builds.
func SetNowFuncForTest(r *Rotator, fn func() time.Time) {
	r.now = fn
}
