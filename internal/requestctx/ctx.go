// Package requestctx stores per-request auth values on context.Context for code
// that only receives context (e.g. format handlers and base.Store).
package requestctx

import "context"

type userIDKey struct{}
type usernameKey struct{}

// WithUser returns ctx augmented with the authenticated user's id and username.
// Empty strings are ignored (no key set).
func WithUser(ctx context.Context, userID, username string) context.Context {
	if userID != "" {
		ctx = context.WithValue(ctx, userIDKey{}, userID)
	}
	if username != "" {
		ctx = context.WithValue(ctx, usernameKey{}, username)
	}
	return ctx
}

// UserID returns the authenticated user's UUID, if set.
func UserID(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey{}).(string)
	return v
}

// Username returns the authenticated user's login name, if set.
func Username(ctx context.Context) string {
	v, _ := ctx.Value(usernameKey{}).(string)
	return v
}
