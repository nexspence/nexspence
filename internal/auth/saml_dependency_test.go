package auth_test

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

// GO-2026-6909: the SAML library read a <NameID> from any XML namespace into
// the authenticated subject, so a foreign-namespace element inside a
// validly signed assertion could name another principal. It also
// dereferenced an assertion's optional <Subject> and <Conditions> without a
// nil check. Both are fixed upstream from v0.6.0 on.
//
// The Go vulnerability database still lists every version as affected, so the
// govulncheck gate ignores this one advisory — these two tests are the signal
// that the dependency is not vulnerable and not downgraded.

// A <NameID> in a foreign namespace is not the subject: the parser has to
// ignore it instead of accepting it as the authenticated name.
func TestSAMLDependency_IgnoresForeignNamespaceNameID(t *testing.T) {
	const doc = `<saml:Subject xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ` +
		`xmlns:evil="urn:evil"><evil:NameID>attacker@evil</evil:NameID></saml:Subject>`

	var subject saml.Subject
	require.NoError(t, xml.Unmarshal([]byte(doc), &subject))
	assert.Nil(t, subject.NameID,
		"a NameID from a foreign XML namespace must not become the authenticated subject")
}

// The dependency floor: the library has to stay at v0.6.0 or newer, where the
// namespace-blind NameID and the unchecked nil dereferences are fixed. The
// govulncheck ignore would otherwise hide a downgrade of this dependency.
func TestSAMLDependency_VersionIsNotVulnerable(t *testing.T) {
	const floor = "v0.6.0"

	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	require.NoError(t, err)

	var found string
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "github.com/crewjam/saml" {
			found = fields[1]
			break
		}
	}
	require.NotEmpty(t, found, "go.mod must require github.com/crewjam/saml")

	assert.True(t, semver.Compare(found, floor) >= 0,
		"github.com/crewjam/saml %s is vulnerable to GO-2026-6909; %s or newer is required",
		found, floor)
}
