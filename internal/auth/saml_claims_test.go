package auth

import (
	"testing"

	"github.com/crewjam/saml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/config"
)

// internal (package auth, not auth_test) so it can reach the unexported
// extractClaims — these tests build the real crewjam/saml Assertion struct
// production code parses assertions into, not a hand-rolled stand-in.

const claimsTestIdPMetaXML = `<?xml version="1.0"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com">
  <IDPSSODescriptor WantAuthnRequestsSigned="false"
    protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect"
      Location="https://idp.example.com/sso"/>
  </IDPSSODescriptor>
</EntityDescriptor>`

func newClaimsTestSAMLService(t *testing.T) *SAMLService {
	t.Helper()
	svc, err := NewSAMLService(config.SAMLConfig{
		Enabled:           true,
		SPEntityID:        "https://sp.example.com/saml",
		ACSURL:            "https://sp.example.com/api/v1/auth/saml/acs",
		IDPMetadataXML:    claimsTestIdPMetaXML,
		EmailAttribute:    "email",
		UsernameAttribute: "uid",
		NameAttribute:     "displayName",
		GroupsAttribute:   "groups",
	})
	require.NoError(t, err)
	return svc
}

func namedAttrStatement(name string, values ...string) saml.AttributeStatement {
	vals := make([]saml.AttributeValue, 0, len(values))
	for _, v := range values {
		vals = append(vals, saml.AttributeValue{Value: v})
	}
	return saml.AttributeStatement{
		Attributes: []saml.Attribute{{Name: name, Values: vals}},
	}
}

func TestSAMLService_ExtractClaims_NoGroupsAttribute_GroupsPresentFalse(t *testing.T) {
	svc := newClaimsTestSAMLService(t)
	a := &saml.Assertion{
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "alice@idp"}},
		AttributeStatements: []saml.AttributeStatement{
			namedAttrStatement("email", "alice@example.com"),
			namedAttrStatement("uid", "alice"),
			// no "groups" attribute anywhere in the assertion
		},
	}
	claims := svc.extractClaims(a)
	assert.False(t, claims.GroupsPresent, "assertion has no groups attribute at all")
	assert.Empty(t, claims.Groups)
	assert.Equal(t, "alice@example.com", claims.Email)
}

func TestSAMLService_ExtractClaims_GroupsAttributePresentButEmpty_GroupsPresentTrue(t *testing.T) {
	svc := newClaimsTestSAMLService(t)
	a := &saml.Assertion{
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "alice@idp"}},
		AttributeStatements: []saml.AttributeStatement{
			namedAttrStatement("email", "alice@example.com"),
			namedAttrStatement("uid", "alice"),
			{Attributes: []saml.Attribute{{Name: "groups", Values: nil}}}, // present, zero values
		},
	}
	claims := svc.extractClaims(a)
	assert.True(t, claims.GroupsPresent, "an empty-but-present attribute is still real signal from the IdP")
	assert.Empty(t, claims.Groups)
}

func TestSAMLService_ExtractClaims_GroupsAttributeWithValues(t *testing.T) {
	svc := newClaimsTestSAMLService(t)
	a := &saml.Assertion{
		Subject: &saml.Subject{NameID: &saml.NameID{Value: "alice@idp"}},
		AttributeStatements: []saml.AttributeStatement{
			namedAttrStatement("email", "alice@example.com"),
			namedAttrStatement("groups", "developers", "nexspence-admins"),
		},
	}
	claims := svc.extractClaims(a)
	assert.True(t, claims.GroupsPresent)
	assert.ElementsMatch(t, []string{"developers", "nexspence-admins"}, claims.Groups)
}
