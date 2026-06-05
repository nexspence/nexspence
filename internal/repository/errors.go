package repository

import "errors"

// ErrNotFound is returned by repository lookup methods (Get/GetByID/GetByName/…)
// when no matching row exists. Callers should test for it with errors.Is and
// translate it into the appropriate not-found behavior (e.g. HTTP 404).
var ErrNotFound = errors.New("not found")
