package repository

import "errors"

// ErrNotFound is returned by repository lookup methods (Get/GetByID/GetByName/…)
// when no matching row exists. Callers should test for it with errors.Is and
// translate it into the appropriate not-found behavior (e.g. HTTP 404).
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists is returned by repository write methods when the row would
// violate a uniqueness constraint (e.g. a second user with the same email).
// Callers translate it into the appropriate conflict behavior (e.g. HTTP 409)
// rather than letting a raw driver error surface as a 500 carrying SQL internals.
var ErrAlreadyExists = errors.New("already exists")

// UniqueViolationError is an ErrAlreadyExists that names the field which
// collided, so callers can report which one without parsing driver text.
type UniqueViolationError struct {
	Field string
}

func (e *UniqueViolationError) Error() string { return e.Field + " already exists" }

// Is makes every UniqueViolationError match ErrAlreadyExists, so callers that
// only care that the row exists can test for the sentinel.
func (e *UniqueViolationError) Is(target error) bool { return target == ErrAlreadyExists }
