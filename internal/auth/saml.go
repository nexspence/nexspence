package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"github.com/nexspence-oss/nexspence/internal/config"
)

// SAMLClaims is the normalized user info extracted from a validated SAML assertion.
type SAMLClaims struct {
	Subject  string
	Email    string
	Username string
	Name     string
	Groups   []string
	RawAttrs map[string][]string
}

// SAMLAuthenticator is the interface for SAML SP operations (enables mocking).
type SAMLAuthenticator interface {
	MetadataXML() ([]byte, error)
	AuthnRequestURL(relayState string) (string, error)
	ParseResponse(r *http.Request) (*SAMLClaims, error)
	SignRelayState(returnTo string) string
	VerifyRelayState(rs string) (string, error)
}

// SAMLService implements SAMLAuthenticator using crewjam/saml.
type SAMLService struct {
	sp      saml.ServiceProvider
	cfg     config.SAMLConfig
	hmacKey []byte
}

// NewSAMLService constructs a configured SAMLService.
// SP key pair: loads from cfg.SPCertPEM/SPKeyPEM; generates ephemeral RSA-2048 if either is empty.
// IdP metadata: fetches from cfg.IDPMetadataURL or parses cfg.IDPMetadataXML.
func NewSAMLService(cfg config.SAMLConfig) (*SAMLService, error) {
	privKey, cert, err := loadOrGenerateKeyPair(cfg.SPKeyPEM, cfg.SPCertPEM)
	if err != nil {
		return nil, fmt.Errorf("saml sp key pair: %w", err)
	}

	idpMeta, err := loadIDPMetadata(cfg)
	if err != nil {
		return nil, fmt.Errorf("saml idp metadata: %w", err)
	}

	hmacKey, err := loadOrGenerateHMACKey(cfg.HMACKey)
	if err != nil {
		return nil, fmt.Errorf("saml hmac key: %w", err)
	}

	metaURL, err := url.Parse(cfg.SPEntityID)
	if err != nil {
		return nil, fmt.Errorf("saml sp_entity_id invalid URL: %w", err)
	}
	acsURL, err := url.Parse(cfg.ACSURL)
	if err != nil {
		return nil, fmt.Errorf("saml acs_url invalid URL: %w", err)
	}

	sp := saml.ServiceProvider{
		Key:         privKey,
		Certificate: cert,
		MetadataURL: *metaURL,
		AcsURL:      *acsURL,
		IDPMetadata: idpMeta,
	}

	return &SAMLService{sp: sp, cfg: cfg, hmacKey: hmacKey}, nil
}

// MetadataXML returns the SP metadata as an XML document for IdP registration.
func (s *SAMLService) MetadataXML() ([]byte, error) {
	meta := s.sp.Metadata()
	return xml.MarshalIndent(meta, "", "  ")
}

// AuthnRequestURL builds a redirect-binding AuthnRequest and returns the IdP redirect URL.
func (s *SAMLService) AuthnRequestURL(relayState string) (string, error) {
	u, err := s.sp.MakeRedirectAuthenticationRequest(relayState)
	if err != nil {
		return "", fmt.Errorf("saml authn request: %w", err)
	}
	return u.String(), nil
}

// ParseResponse validates the POST-binding SAMLResponse from the IdP.
func (s *SAMLService) ParseResponse(r *http.Request) (*SAMLClaims, error) {
	assertion, err := s.sp.ParseResponse(r, nil)
	if err != nil {
		return nil, fmt.Errorf("saml parse response: %w", err)
	}
	return s.extractClaims(assertion), nil
}

// SignRelayState encodes returnTo as base64url(json)."."base64url(HMAC-SHA256).
func (s *SAMLService) SignRelayState(returnTo string) string {
	data, _ := json.Marshal(map[string]string{"return_to": returnTo})
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write(data)
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// VerifyRelayState decodes and verifies the relay state. Returns the returnTo path.
func (s *SAMLService) VerifyRelayState(rs string) (string, error) {
	parts := strings.SplitN(rs, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("saml: invalid relay state format")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("saml: relay state data decode: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("saml: relay state sig decode: %w", err)
	}
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write(data)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", fmt.Errorf("saml: relay state signature invalid")
	}
	var payload map[string]string
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("saml: relay state payload decode: %w", err)
	}
	return payload["return_to"], nil
}

func (s *SAMLService) extractClaims(a *saml.Assertion) *SAMLClaims {
	raw := make(map[string][]string)
	for _, stmt := range a.AttributeStatements {
		for _, attr := range stmt.Attributes {
			key := attr.Name
			if attr.FriendlyName != "" {
				key = attr.FriendlyName
			}
			vals := make([]string, 0, len(attr.Values))
			for _, v := range attr.Values {
				vals = append(vals, v.Value)
			}
			raw[key] = vals
			if attr.FriendlyName != "" && attr.Name != "" {
				raw[attr.Name] = vals
			}
		}
	}
	getFirst := func(names ...string) string {
		for _, name := range names {
			if vals, ok := raw[name]; ok && len(vals) > 0 {
				return vals[0]
			}
		}
		return ""
	}

	subject := ""
	if a.Subject != nil && a.Subject.NameID != nil {
		subject = a.Subject.NameID.Value
	}

	username := getFirst(s.cfg.UsernameAttribute)
	if username == "" {
		username = subject
	}

	return &SAMLClaims{
		Subject:  subject,
		Email:    getFirst(s.cfg.EmailAttribute),
		Username: username,
		Name:     getFirst(s.cfg.NameAttribute),
		Groups:   raw[s.cfg.GroupsAttribute],
		RawAttrs: raw,
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func loadOrGenerateKeyPair(keyPEM, certPEM string) (*rsa.PrivateKey, *x509.Certificate, error) {
	if keyPEM == "" || certPEM == "" {
		return generateEphemeralKeyPair()
	}
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, nil, fmt.Errorf("sp_key_pem: invalid PEM")
	}
	privKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, nil, fmt.Errorf("sp_key_pem parse: %w", err)
		}
		var ok bool
		privKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("sp_key_pem: not an RSA key")
		}
	}
	cblock, _ := pem.Decode([]byte(certPEM))
	if cblock == nil {
		return nil, nil, fmt.Errorf("sp_cert_pem: invalid PEM")
	}
	cert, err := x509.ParseCertificate(cblock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("sp_cert_pem parse: %w", err)
	}
	return privKey, cert, nil
}

func generateEphemeralKeyPair() (*rsa.PrivateKey, *x509.Certificate, error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Nexspence SAML SP"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, err
	}
	return privKey, cert, nil
}

func loadIDPMetadata(cfg config.SAMLConfig) (*saml.EntityDescriptor, error) {
	var xmlData []byte
	if cfg.IDPMetadataURL != "" {
		resp, err := http.Get(cfg.IDPMetadataURL) //nolint:noctx
		if err != nil {
			return nil, fmt.Errorf("fetch %s: %w", cfg.IDPMetadataURL, err)
		}
		defer resp.Body.Close()
		xmlData, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("read idp metadata: %w", err)
		}
	} else {
		xmlData = []byte(cfg.IDPMetadataXML)
	}
	var meta saml.EntityDescriptor
	if err := xml.Unmarshal(xmlData, &meta); err != nil {
		return nil, fmt.Errorf("parse idp metadata XML: %w", err)
	}
	return &meta, nil
}

func loadOrGenerateHMACKey(encoded string) ([]byte, error) {
	if encoded == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		return key, nil
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("hmac_key base64 decode: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("hmac_key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}
