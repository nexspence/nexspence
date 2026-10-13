package oci

import (
	"bufio"
	"bytes"
	"crypto/md5"  //nolint:gosec // artifact checksum
	"crypto/sha1" //nolint:gosec // artifact checksum
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// manifestBody is a request-scoped snapshot, never shared state. Hashing and
// JSON validation finish before persistence or a success response. Temporary
// files keep memory bounded even for a single enormous JSON string.
type manifestBody struct {
	file              *os.File
	size              int64
	sha256, sha1, md5 string
	meta              manifestMeta
	mediaType         string
}

func spoolManifest(r io.Reader) (_ *manifestBody, err error) {
	f, err := os.CreateTemp("", "oci-manifest-*")
	if err != nil {
		return nil, fmt.Errorf("create manifest spool: %w", err)
	}
	body := &manifestBody{file: f}
	defer func() {
		if err != nil {
			body.Close()
		}
	}()
	s256, s1, m5 := sha256.New(), sha1.New(), md5.New() //nolint:gosec // artifact checksums
	body.size, err = io.Copy(io.MultiWriter(f, s256, s1, m5), r)
	if err != nil {
		return nil, fmt.Errorf("spool manifest: %w", err)
	}
	body.sha256, body.sha1, body.md5 = fmt.Sprintf("%x", s256.Sum(nil)), fmt.Sprintf("%x", s1.Sum(nil)), fmt.Sprintf("%x", m5.Sum(nil))
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind manifest: %w", err)
	}
	if body.size <= maxManifestBytes {
		// Keep the established metadata semantics for ordinary and hosted bodies.
		raw, readErr := io.ReadAll(body.reader())
		if readErr != nil {
			return nil, fmt.Errorf("read manifest spool: %w", readErr)
		}
		if _, err = manifestContentType(raw, ""); err != nil {
			return nil, err
		}
		var doc struct {
			MediaType string `json:"mediaType"`
		}
		if err = json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse manifest media type: %w", err)
		}
		body.mediaType = doc.MediaType
		body.meta, _ = parseManifestMeta(raw)
	} else {
		parser := manifestJSON{r: bufio.NewReader(f)}
		if err = parser.document(); err != nil {
			return nil, fmt.Errorf("invalid manifest JSON: %w", err)
		}
		body.meta = parser.meta
		body.mediaType = parser.meta.MediaType
		if body.meta.ArtifactType == "" {
			body.meta.ArtifactType = parser.configType
		}
	}
	return body, nil
}

func (b *manifestBody) Close() {
	if b != nil {
		_ = b.file.Close()
		_ = os.Remove(b.file.Name())
	}
}

func (b *manifestBody) reader() io.Reader {
	return io.NewSectionReader(b.file, 0, b.size)
}

func (b *manifestBody) contentType(header string) (string, error) {
	// Reuse the same negotiation rules without buffering the original document.
	projection, err := json.Marshal(struct {
		MediaType string `json:"mediaType"`
	}{b.mediaType})
	if err != nil {
		return "", fmt.Errorf("encode manifest media type: %w", err)
	}
	return manifestContentType(projection, header)
}

// manifestJSON validates incrementally and retains only descriptive metadata.
// Unknown values, array entries and long strings consume constant memory. The
// limits apply to extracted metadata, never to the manifest's stored bytes.
type manifestJSON struct {
	r          *bufio.Reader
	meta       manifestMeta
	configType string
}

func (p *manifestJSON) space() (byte, error) {
	for {
		c, err := p.r.ReadByte()
		if err != nil {
			return 0, err
		}
		if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return c, nil
		}
	}
}

func (p *manifestJSON) document() error {
	c, err := p.space()
	if err != nil || c != '{' {
		return fmt.Errorf("manifest must be a JSON object")
	}
	if err := p.object("", 0); err != nil {
		return err
	}
	if _, err := p.space(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func (p *manifestJSON) value(path string, depth int) error {
	if depth > 10000 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	c, err := p.space()
	if err != nil {
		return err
	}
	if path == "mediaType" && c != '"' && c != 'n' {
		return fmt.Errorf("mediaType must be a string")
	}
	switch c {
	case '{':
		return p.object(path, depth)
	case '[':
		c, err = p.space()
		if err != nil {
			return err
		}
		if c == ']' {
			return nil
		}
		if err := p.r.UnreadByte(); err != nil {
			return err
		}
		for {
			if err := p.value("?", depth+1); err != nil {
				return err
			}
			c, err = p.space()
			if err != nil {
				return err
			}
			if c == ']' {
				return nil
			}
			if c != ',' {
				return fmt.Errorf("expected array separator")
			}
		}
	case '"':
		s, err := p.stringValue()
		if err != nil {
			return err
		}
		switch path {
		case "mediaType":
			p.meta.MediaType = s
		case "artifactType":
			p.meta.ArtifactType = s
		case "config.mediaType":
			p.configType = s
		case "subject.digest":
			p.meta.Subject = s
		default:
			if key, ok := strings.CutPrefix(path, "annotations."); ok && len(p.meta.Annotations) < 1024 {
				if p.meta.Annotations == nil {
					p.meta.Annotations = map[string]string{}
				}
				p.meta.Annotations[key] = s
			}
		}
		return nil
	case 't':
		return p.literal("rue")
	case 'f':
		return p.literal("alse") //nolint:misspell // tail of the JSON literal false
	case 'n':
		return p.literal("ull")
	default:
		if path == "mediaType" {
			return fmt.Errorf("mediaType must be a string")
		}
		return p.number(c)
	}
}

func (p *manifestJSON) object(path string, depth int) error {
	c, err := p.space()
	if err != nil {
		return err
	}
	if c == '}' {
		return nil
	}
	for {
		if c != '"' {
			return fmt.Errorf("expected object key")
		}
		key, err := p.stringValue()
		if err != nil {
			return err
		}
		c, err = p.space()
		if err != nil || c != ':' {
			return fmt.Errorf("expected object colon")
		}
		child := "?"
		switch path {
		case "":
			switch key {
			case "mediaType", "artifactType", "config", "subject", "annotations":
				child = key
			}
		case "config", "subject", "annotations":
			child = path + "." + key
		}
		if err := p.value(child, depth+1); err != nil {
			return err
		}
		c, err = p.space()
		if err != nil {
			return err
		}
		if c == '}' {
			return nil
		}
		if c != ',' {
			return fmt.Errorf("expected object separator")
		}
		c, err = p.space()
		if err != nil {
			return err
		}
	}
}

func (p *manifestJSON) literal(tail string) error {
	for i := range len(tail) {
		c, err := p.r.ReadByte()
		if err != nil || c != tail[i] {
			return fmt.Errorf("invalid JSON literal")
		}
	}
	return nil
}

func (p *manifestJSON) stringValue() (string, error) {
	// Keep at most 4 KiB per metadata string, including escapes. A value that
	// exceeds this bound is validated but omitted from descriptive metadata.
	var raw bytes.Buffer
	raw.WriteByte('"')
	oversized := false
	for {
		c, err := p.r.ReadByte()
		if err != nil {
			return "", err
		}
		if c < 0x20 {
			return "", fmt.Errorf("invalid JSON string")
		}
		if !oversized {
			raw.WriteByte(c)
			oversized = raw.Len() > 4096
		}
		if c == '"' {
			break
		}
		if c != '\\' {
			continue
		}
		c, err = p.r.ReadByte()
		if err != nil {
			return "", err
		}
		if !oversized {
			raw.WriteByte(c)
		}
		if c == 'u' {
			for range 4 {
				c, err = p.r.ReadByte()
				if err != nil || !strings.ContainsRune("0123456789abcdefABCDEF", rune(c)) {
					return "", fmt.Errorf("invalid Unicode escape")
				}
				if !oversized {
					raw.WriteByte(c)
				}
			}
		} else if !strings.ContainsRune(`"\/bfnrt`, rune(c)) {
			return "", fmt.Errorf("invalid JSON escape")
		}
	}
	if oversized {
		return "", nil
	}
	var s string
	err := json.Unmarshal(raw.Bytes(), &s)
	return s, err
}

func (p *manifestJSON) number(first byte) error {
	// Columns: zero, nonzero digit, minus, dot, exponent, plus, delimiter.
	// -1 rejects the token; -2 finishes a valid number without consuming its
	// delimiter. This grammar accepts arbitrary lengths with bounded memory.
	transitions := [9][7]int{
		{2, 3, 1, -1, -1, -1, -1},
		{2, 3, -1, -1, -1, -1, -1},
		{-1, -1, -1, 4, 6, -1, -2},
		{3, 3, -1, 4, 6, -1, -2},
		{5, 5, -1, -1, -1, -1, -1},
		{5, 5, -1, -1, 6, -1, -2},
		{8, 8, 7, -1, -1, 7, -1},
		{8, 8, -1, -1, -1, -1, -1},
		{8, 8, -1, -1, -1, -1, -2},
	}
	state, c := 0, first
	for {
		class := numberClass(c)
		if class < 0 {
			return fmt.Errorf("invalid JSON number")
		}
		state = transitions[state][class]
		if state == -1 {
			return fmt.Errorf("invalid JSON number")
		}
		if state == -2 {
			return p.r.UnreadByte()
		}
		var err error
		c, err = p.r.ReadByte()
		if err != nil {
			return err
		}
	}
}

func numberClass(c byte) int {
	switch {
	case c == '0':
		return 0
	case c >= '1' && c <= '9':
		return 1
	case c == '-':
		return 2
	case c == '.':
		return 3
	case c == 'e' || c == 'E':
		return 4
	case c == '+':
		return 5
	case strings.ContainsRune(" \t\r\n,]}", rune(c)):
		return 6
	default:
		return -1
	}
}
