package terraform

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

// Signer signs the checksum document of a hosted provider version.
//
// Terraform's registry client installs a provider only when it can verify the
// checksum document against a detached OpenPGP signature made by a key the
// registry publishes: without one it refuses the package with
// "authentication signature from unknown issuer". The key is therefore a
// deployment setting rather than a per-repository one — every provider this
// instance serves is signed with it.
type Signer struct {
	entity    *openpgp.Entity
	publicKey string
}

// LoadSigner reads the key at keyPath and returns the signer it describes.
//
// The file is the armored export of a private key (the output of
// `gpg --export-secret-keys --armor`). A binary key ring is accepted too: an
// operator who exported one without --armor should get a clear error rather
// than a registry that silently serves unsigned packages.
func LoadSigner(keyPath string) (*Signer, error) {
	// The path is the deployment's own setting, not anything a request
	// carries: there is no untrusted path to include here.
	raw, err := os.ReadFile(keyPath) //nolint:gosec // G304: the path comes from the operator's configuration
	if err != nil {
		return nil, fmt.Errorf("read terraform signing key %s: %w", keyPath, err)
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(raw))
	if err != nil {
		entities, err = openpgp.ReadKeyRing(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("parse terraform signing key %s: %w", keyPath, err)
		}
	}
	for _, entity := range entities {
		if entity.PrivateKey == nil {
			continue
		}
		publicKey, err := armorPublicKey(entity)
		if err != nil {
			return nil, fmt.Errorf("export the public key of terraform signing key %s: %w", keyPath, err)
		}
		return &Signer{entity: entity, publicKey: publicKey}, nil
	}
	return nil, fmt.Errorf("terraform signing key %s holds no private key", keyPath)
}

// SignDetached returns the detached signature over document.
//
// The signature is binary, not armored: the Terraform client verifies it with
// openpgp.CheckDetachedSignature, which reads the binary form and rejects the
// armored one with "openpgp: invalid data: tag byte does not have MSB set".
//
// The signature carries the moment it was made, so two signatures over the
// same bytes differ. The caller must therefore serve one signature per
// document — a client that verifies the second signature against the first
// document fails.
func (s *Signer) SignDetached(document []byte) ([]byte, error) {
	if s == nil || s.entity == nil {
		return nil, errors.New("no terraform signing key is configured")
	}
	var buf bytes.Buffer
	if err := openpgp.DetachSign(&buf, s.entity, bytes.NewReader(document), nil); err != nil {
		return nil, fmt.Errorf("sign the terraform checksum document: %w", err)
	}
	return buf.Bytes(), nil
}

// PublicKey is the armored public half of the signing key: the registry
// publishes it in the download document, and the client verifies the
// signature with it.
func (s *Signer) PublicKey() string {
	if s == nil {
		return ""
	}
	return s.publicKey
}

// armorPublicKey is the armored public key of an entity — what the registry
// hands the client in the download document's signing_keys.
func armorPublicKey(entity *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return "", err
	}
	if err := entity.Serialize(w); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
