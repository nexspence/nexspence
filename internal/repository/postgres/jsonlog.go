package postgres

import (
	"encoding/json"
	"log"
)

// unmarshalJSONB decodes a JSONB column into dest, logging — rather than
// discarding — a decode failure.
//
// A malformed column used to leave dest at its zero value with nothing written
// anywhere, which is how a corrupted rbac `actions` array could silently turn a
// privilege into one that grants nothing. The read still succeeds: a row that is
// otherwise usable should not be lost because one column is unreadable, so this
// makes the failure visible instead of fatal.
//
// An empty or NULL column is not a failure — it is an absent value, and dest
// keeps its zero value without a log line.
func unmarshalJSONB(raw []byte, dest any, table, id, field string) {
	if len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		log.Printf("nexor: db: malformed json column table=%s id=%s field=%s err=%v", table, id, field, err)
	}
}
