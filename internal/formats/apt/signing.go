package apt

import (
	"bytes"
	"crypto"
	"fmt"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// Signing is configured per repository:
//
//	formatConfig.signing_key            ASCII-armored private key
//	formatConfig.signing_key_passphrase passphrase, when the key is protected
//
// Without a key the repository stays unsigned and only works for apt sources
// marked [trusted=yes] — which is what #103 is about.
const (
	signingKeyField     = "signing_key"
	signingKeyPassField = "signing_key_passphrase"
)

func configString(repo *domain.Repository, field string) string {
	if repo == nil || repo.FormatConfig == nil {
		return ""
	}
	s, _ := repo.FormatConfig[field].(string)
	return strings.TrimSpace(s)
}

// signingConfigured reports whether the repository has a signing key at all.
func signingConfigured(repo *domain.Repository) bool {
	return configString(repo, signingKeyField) != ""
}

// signingEntity parses (and, if needed, unlocks) the configured private key.
func signingEntity(repo *domain.Repository) (*openpgp.Entity, error) {
	armored := configString(repo, signingKeyField)
	if armored == "" {
		return nil, fmt.Errorf("no signing key configured")
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	if len(ring) == 0 {
		return nil, fmt.Errorf("signing key: no key found")
	}
	entity := ring[0]
	if entity.PrivateKey == nil {
		return nil, fmt.Errorf("signing key: public key only, cannot sign")
	}
	pass := []byte(configString(repo, signingKeyPassField))
	if entity.PrivateKey.Encrypted {
		if err := entity.PrivateKey.Decrypt(pass); err != nil {
			return nil, fmt.Errorf("signing key: %w", err)
		}
	}
	for _, sub := range entity.Subkeys {
		if sub.PrivateKey != nil && sub.PrivateKey.Encrypted {
			// A subkey we cannot unlock is not fatal: the primary key signs.
			_ = sub.PrivateKey.Decrypt(pass)
		}
	}
	return entity, nil
}

// signingConfig pins the digest to SHA-256; apt rejects SHA-1 signatures.
func signingConfig() *packet.Config {
	return &packet.Config{DefaultHash: crypto.SHA256}
}

// clearSign wraps the document in an inline signature — the InRelease format.
func clearSign(repo *domain.Repository, body []byte) ([]byte, error) {
	entity, err := signingEntity(repo)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, entity.PrivateKey, signingConfig())
	if err != nil {
		return nil, fmt.Errorf("clearsign: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return nil, fmt.Errorf("clearsign: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("clearsign: %w", err)
	}
	// A trailing newline keeps the armor block well-formed for apt's parser.
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// detachSign produces the Release.gpg counterpart of a Release document.
func detachSign(repo *domain.Repository, body []byte) ([]byte, error) {
	entity, err := signingEntity(repo)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, entity, bytes.NewReader(body), signingConfig()); err != nil {
		return nil, fmt.Errorf("detached sign: %w", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// armoredPublicKey exports the public half so clients can trust the repository.
func armoredPublicKey(repo *domain.Repository) ([]byte, error) {
	entity, err := signingEntity(repo)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if err := entity.Serialize(w); err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
