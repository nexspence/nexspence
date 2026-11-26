package terraform_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
)

// The signing key of a test is generated here and never committed: a real
// key in a repository is a credential nobody can rotate.
var (
	testKeyOnce   sync.Once
	testKeyEntity *openpgp.Entity
	testKeyErr    error
)

// testKey is a throwaway OpenPGP key, generated once for the whole test
// binary — RSA key generation costs seconds, and every test signs with the
// same key.
func testKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	testKeyOnce.Do(func() {
		testKeyEntity, testKeyErr = openpgp.NewEntity("Nexspence Test", "", "test@nexspence.invalid", nil)
	})
	require.NoError(t, testKeyErr)
	return testKeyEntity
}

// armoredPrivateKey writes the throwaway key to a temp file the way an
// operator would export it (gpg --export-secret-keys --armor).
func armoredPrivateKey(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	require.NoError(t, err)
	require.NoError(t, testKey(t).SerializePrivate(w, nil))
	require.NoError(t, w.Close())

	path := filepath.Join(t.TempDir(), "terraform-signing-key.asc")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// binaryPrivateKey writes the same key without the armor block: an operator
// who exported it that way must get a working registry, not a parse error.
func binaryPrivateKey(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, testKey(t).SerializePrivate(&buf, nil))

	path := filepath.Join(t.TempDir(), "terraform-signing-key.pgp")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// armoredPublicKey writes the public half of the throwaway key.
func armoredPublicKey(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	require.NoError(t, err)
	require.NoError(t, testKey(t).Serialize(w))
	require.NoError(t, w.Close())

	path := filepath.Join(t.TempDir(), "terraform-signing-key.pub.asc")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// The published public key must verify the signature the registry serves:
// that is the whole point of publishing it.
func TestLoadSigner_SignedDocumentVerifiesAgainstThePublishedKey(t *testing.T) {
	signer, err := terraform.LoadSigner(armoredPrivateKey(t))
	require.NoError(t, err)

	document := []byte("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855  terraform-provider-widget_1.0.0_linux_amd64.zip\n")
	signature, err := signer.SignDetached(document)
	require.NoError(t, err)
	require.NotEmpty(t, signature)

	assert.Contains(t, signer.PublicKey(), "BEGIN PGP PUBLIC KEY BLOCK",
		"the registry publishes the public key in armored form")

	keyring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(signer.PublicKey()))
	require.NoError(t, err)
	require.NotEmpty(t, keyring)

	verified, err := openpgp.CheckDetachedSignature(keyring,
		bytes.NewReader(document), bytes.NewReader(signature), nil)
	require.NoError(t, err)
	require.NotNil(t, verified)
}

// A key exported without the armor block is a key the operator has, not a
// misconfiguration they have to undo.
func TestLoadSigner_BinaryKeyRingIsAccepted(t *testing.T) {
	signer, err := terraform.LoadSigner(binaryPrivateKey(t))
	require.NoError(t, err)

	_, err = signer.SignDetached([]byte("document"))
	require.NoError(t, err)
}

// A key file that is not there is an operator error, and the message has to
// name the file.
func TestLoadSigner_MissingFile_Fails(t *testing.T) {
	_, err := terraform.LoadSigner(filepath.Join(t.TempDir(), "absent.asc"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent.asc")
}

// A file that is not a key must not be accepted: a registry that signs with
// something it did not parse would serve unverifiable packages.
func TestLoadSigner_UnparsableFile_Fails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-key.asc")
	require.NoError(t, os.WriteFile(path, []byte("this is not a key"), 0o600))

	_, err := terraform.LoadSigner(path)
	require.Error(t, err)
}

// A public key cannot sign. Serving a registry that cannot produce a
// signature is worse than refusing the key.
func TestLoadSigner_PublicKeyOnly_Fails(t *testing.T) {
	_, err := terraform.LoadSigner(armoredPublicKey(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private key")
}

// The handler asks a signer that may not be there (no key configured): the
// calls have to be safe, not a nil-pointer panic in the middle of a request.
func TestSigner_NilSignerIsSafe(t *testing.T) {
	var signer *terraform.Signer

	_, err := signer.SignDetached([]byte("document"))
	require.Error(t, err)
	assert.Empty(t, signer.PublicKey())
}
