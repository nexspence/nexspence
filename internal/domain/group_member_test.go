package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupMemberNames(t *testing.T) {
	t.Parallel()
	r := &Repository{
		FormatConfig: map[string]any{
			"member_names": []any{"a", "b"},
		},
	}
	require.Equal(t, []string{"a", "b"}, GroupMemberNames(r))

	r2 := &Repository{
		FormatConfig: map[string]any{
			"member_names": []string{"x"},
		},
	}
	require.Equal(t, []string{"x"}, GroupMemberNames(r2))

	require.Nil(t, GroupMemberNames(nil))
	require.Nil(t, GroupMemberNames(&Repository{}))
}
