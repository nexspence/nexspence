package auth

import "context"

// GroupLookup answers "which groups is this user in?" from a directory,
// for identity providers whose id_token never carries a groups claim
// (Google Workspace). LoginOIDC feeds the answer into the same admin_group
// / role_mappings pipeline a groups claim would take.
type GroupLookup interface {
	// Groups returns the group identifiers (for Google: group email addresses)
	// the user with the given email belongs to. An error means "no answer",
	// not "no groups" — callers must leave existing role assignments alone.
	Groups(ctx context.Context, email string) ([]string, error)
}
