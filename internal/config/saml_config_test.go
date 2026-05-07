package config_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateSAML_Disabled_PassesAlways(t *testing.T) {
	err := config.ValidateSAML(config.SAMLConfig{Enabled: false})
	require.NoError(t, err)
}

func TestValidateSAML_Enabled_RequiresEntityAndACS(t *testing.T) {
	err := config.ValidateSAML(config.SAMLConfig{
		Enabled:        true,
		IDPMetadataURL: "https://idp/meta",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sp_entity_id")
}

func TestValidateSAML_Enabled_RequiresIDPMetadata(t *testing.T) {
	err := config.ValidateSAML(config.SAMLConfig{
		Enabled:    true,
		SPEntityID: "https://sp/saml",
		ACSURL:     "https://sp/acs",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "idp_metadata")
}

func TestValidateSAML_Enabled_AllowlistRequiresNonEmpty(t *testing.T) {
	err := config.ValidateSAML(config.SAMLConfig{
		Enabled:        true,
		SPEntityID:     "https://sp/saml",
		ACSURL:         "https://sp/acs",
		IDPMetadataURL: "https://idp/meta",
		Provisioning:   "allowlist",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "email_allowlist")
}

func TestValidateSAML_Enabled_Valid(t *testing.T) {
	err := config.ValidateSAML(config.SAMLConfig{
		Enabled:        true,
		SPEntityID:     "https://sp/saml",
		ACSURL:         "https://sp/acs",
		IDPMetadataURL: "https://idp/meta",
		Provisioning:   "jit",
	})
	require.NoError(t, err)
}
