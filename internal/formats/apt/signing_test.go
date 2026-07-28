package apt_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newSigningKey mints a throwaway key and returns it ASCII-armored, plus the
// entity list to verify signatures against.
func newSigningKey(t *testing.T) (armored string, keyring openpgp.EntityList) {
	t.Helper()
	entity, err := openpgp.NewEntity("Nexspence Test", "apt signing", "apt@example.test", nil)
	require.NoError(t, err)

	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	require.NoError(t, err)
	require.NoError(t, entity.SerializePrivateWithoutSigning(w, nil))
	require.NoError(t, w.Close())

	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(buf.String()))
	require.NoError(t, err)
	return buf.String(), ring
}

// signedRepo is an apt repository configured to sign its Release files.
func signedRepo(t *testing.T, name string) (*domain.Repository, openpgp.EntityList) {
	t.Helper()
	armored, ring := newSigningKey(t)
	repo := testutil.SimpleRepo(name, "apt")
	repo.FormatConfig = map[string]any{"signing_key": armored}
	return repo, ring
}

// A default apt client rejects an unsigned repository, so InRelease must carry
// an inline signature when a signing key is configured (#103).
func TestApt_InRelease_IsClearsigned(t *testing.T) {
	repo, ring := signedRepo(t, "debs-signed")
	r := setup(repo)
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-signed", "/pool/main/hello_1.0_amd64.deb", "deb-bytes"))

	w := get(t, r, "/repository/debs-signed/dists/stable/InRelease")
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.Bytes()
	require.True(t, bytes.HasPrefix(body, []byte("-----BEGIN PGP SIGNED MESSAGE-----")),
		"InRelease must be clearsigned, got: %.60s", body)

	block, rest := clearsign.Decode(body)
	require.NotNil(t, block, "clearsign block must parse (trailing: %q)", string(rest))
	assert.Contains(t, string(block.Plaintext), "Suite: stable")
	assert.Contains(t, string(block.Plaintext), "SHA256:")

	_, err := openpgp.CheckDetachedSignature(ring, bytes.NewReader(block.Bytes), block.ArmoredSignature.Body, nil)
	assert.NoError(t, err, "signature must verify against the configured key")
}

// apt fetches Release and Release.gpg separately, so the detached signature has
// to verify against the body served by the other request.
func TestApt_ReleaseGPG_VerifiesAgainstRelease(t *testing.T) {
	repo, ring := signedRepo(t, "debs-detached")
	r := setup(repo)
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-detached", "/pool/main/hello_1.0_amd64.deb", "deb-bytes"))

	release := get(t, r, "/repository/debs-detached/dists/stable/Release")
	require.Equal(t, http.StatusOK, release.Code)

	sig := get(t, r, "/repository/debs-detached/dists/stable/Release.gpg")
	require.Equal(t, http.StatusOK, sig.Code)
	assert.True(t, strings.HasPrefix(sig.Body.String(), "-----BEGIN PGP SIGNATURE-----"),
		"Release.gpg must be an armored detached signature, got: %.60s", sig.Body.String())

	_, err := openpgp.CheckArmoredDetachedSignature(ring,
		bytes.NewReader(release.Body.Bytes()), strings.NewReader(sig.Body.String()), nil)
	assert.NoError(t, err)
}

// The signature is generated over one snapshot of Release while apt verifies it
// against another fetch, so the file has to be byte-identical across requests.
func TestApt_Release_IsStableAcrossRequests(t *testing.T) {
	repo := testutil.SimpleRepo("debs-stable", "apt")
	r := setup(repo)
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-stable", "/pool/main/hello_1.0_amd64.deb", "deb-bytes"))

	first := get(t, r, "/repository/debs-stable/dists/stable/Release")
	// apt fetches Release and its signature seconds apart; a wall-clock Date
	// makes the two disagree and verification fails.
	time.Sleep(1100 * time.Millisecond)
	second := get(t, r, "/repository/debs-stable/dists/stable/Release")
	require.Equal(t, http.StatusOK, first.Code)
	assert.Equal(t, first.Body.String(), second.Body.String(),
		"Release must not change between requests (its Date must not be 'now')")
}

// Without a key nothing can be signed: Release.gpg has no answer, and InRelease
// keeps serving the plain document (what apt [trusted=yes] sources rely on).
func TestApt_Signing_NotConfigured(t *testing.T) {
	repo := testutil.SimpleRepo("debs-unsigned", "apt")
	r := setup(repo)
	require.Equal(t, http.StatusCreated, putDeb(r, "debs-unsigned", "/pool/main/hello_1.0_amd64.deb", "deb-bytes"))

	assert.Equal(t, http.StatusNotFound, get(t, r, "/repository/debs-unsigned/dists/stable/Release.gpg").Code)
	assert.Equal(t, http.StatusNotFound, get(t, r, "/repository/debs-unsigned/public.gpg").Code)

	inRelease := get(t, r, "/repository/debs-unsigned/dists/stable/InRelease")
	require.Equal(t, http.StatusOK, inRelease.Code)
	assert.Contains(t, inRelease.Body.String(), "Suite: stable")
	assert.NotContains(t, inRelease.Body.String(), "BEGIN PGP")
}

// Clients need the public half to trust the repository.
func TestApt_PublicKey_Served(t *testing.T) {
	repo, ring := signedRepo(t, "debs-pubkey")
	r := setup(repo)

	w := get(t, r, "/repository/debs-pubkey/public.gpg")
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, strings.HasPrefix(w.Body.String(), "-----BEGIN PGP PUBLIC KEY BLOCK-----"),
		"expected an armored public key, got: %.60s", w.Body.String())

	served, err := openpgp.ReadArmoredKeyRing(strings.NewReader(w.Body.String()))
	require.NoError(t, err)
	require.Len(t, served, 1)
	assert.Equal(t, ring[0].PrimaryKey.KeyId, served[0].PrimaryKey.KeyId)
	assert.Nil(t, served[0].PrivateKey, "the private key must never be served")
}

// A malformed key must not take the whole repository down.
func TestApt_Signing_BrokenKey_500(t *testing.T) {
	repo := testutil.SimpleRepo("debs-badkey", "apt")
	repo.FormatConfig = map[string]any{"signing_key": "not-a-pgp-key"}
	r := setup(repo)

	assert.Equal(t, http.StatusInternalServerError,
		get(t, r, "/repository/debs-badkey/dists/stable/InRelease").Code)
}
