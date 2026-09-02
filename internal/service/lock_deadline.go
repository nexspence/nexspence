package service

import "time"

// pastDeadline reports whether a long-running job has reached the moment its
// distributed lock expires. Nothing renews those locks, so past that point
// another node can acquire the same lock and start the same work — cleanup, GC
// and blob-store migration each stop there instead of continuing unprotected
// (#371). A zero deadline means the job holds no lock and has nothing to
// outlive.
func pastDeadline(deadline time.Time) bool {
	return !deadline.IsZero() && !time.Now().Before(deadline)
}
