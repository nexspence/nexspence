package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoutingRuleAllows(t *testing.T) {
	var nilRule *RoutingRule
	assert.True(t, nilRule.Allows("/anything"), "nil rule allows everything")

	block := &RoutingRule{Mode: "BLOCK", Matchers: []string{`^/com/acme/`}}
	assert.False(t, block.Allows("/com/acme/lib/1.0/lib-1.0.jar"))
	assert.True(t, block.Allows("/org/apache/x/1.0/x-1.0.jar"))

	allow := &RoutingRule{Mode: "ALLOW", Matchers: []string{`^/releases/`}}
	assert.True(t, allow.Allows("/releases/a.jar"))
	assert.False(t, allow.Allows("/snapshots/a.jar"))

	assert.False(t, (&RoutingRule{Mode: "ALLOW"}).Allows("/x"), "empty ALLOW blocks all")
	assert.True(t, (&RoutingRule{Mode: "BLOCK"}).Allows("/x"), "empty BLOCK allows all")

	bad := &RoutingRule{Mode: "BLOCK", Matchers: []string{`(`, `^/x`}}
	assert.False(t, bad.Allows("/x"), "an invalid matcher is skipped, the rest still apply")
}
