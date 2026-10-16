package formats

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ErrNonCanonicalPath marks a repository path that names an artifact by a
// second spelling. See CanonicalPath.
var ErrNonCanonicalPath = errors.New("non-canonical path")

// nonCanonicalError is an ErrNonCanonicalPath whose text is the reason alone,
// ready to hand to the client.
type nonCanonicalError string

func (e nonCanonicalError) Error() string { return string(e) }

func (nonCanonicalError) Is(target error) bool { return target == ErrNonCanonicalPath }

// maxPathDecodes bounds how many layers of percent-encoding CanonicalPath
// peels off a segment. A segment still decoding after that many layers is no
// artifact name; it is refused rather than half-checked.
const maxPathDecodes = 8

// CanonicalPath returns the path a format handler serves for p, or
// ErrNonCanonicalPath when p reaches that artifact by a second spelling.
//
// Format handlers serve path.Clean of the request path. A check judged on the
// path as requested therefore misses every spelling Clean rewrites: an
// anchored ^/private/ rule does not match "//private/x" or "/./private/x", and
// the handler serves "/private/x" anyway. Those spellings are refused rather
// than rewritten, so no legitimate path changes meaning on the way through: an
// artifact path never has an empty, "." or ".." segment. Segments are also
// percent-decoded, layer by layer: "%2e%2e" is no ".." to path.Clean, but a
// proxy hands it to an upstream that decodes it back to one, and an encoded
// "/" splits a segment the checks here judged whole.
//
// A trailing slash is an ordinary spelling (PyPI's simple index needs it). It
// passes, and is dropped from the result as path.Clean drops it for the
// handler; see RuleAllows for how a rule judges it.
func CanonicalPath(p string) (string, error) {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, seg := range segs {
		if seg == "" {
			if i == len(segs)-1 {
				continue // trailing slash, or the root itself
			}
			return "", nonCanonicalError("path must not contain an empty segment")
		}
		if err := checkSegment(seg); err != nil {
			return "", err
		}
	}
	return path.Clean("/" + strings.TrimPrefix(p, "/")), nil
}

// checkSegment refuses a segment that is, or percent-decodes to, "." or "..",
// or that decodes to something containing "/".
func checkSegment(seg string) error {
	for range maxPathDecodes {
		if seg == "." || seg == ".." {
			return nonCanonicalError(fmt.Sprintf("path must not contain a %q segment", seg))
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil || decoded == seg {
			// No escape left (or a literal "%" in a name that is none):
			// nothing an upstream would decode either.
			return nil //nolint:nilerr // an undecodable segment is a literal name, not a failure
		}
		if strings.Contains(decoded, "/") {
			return nonCanonicalError(`path must not contain an encoded "/"`)
		}
		seg = decoded
	}
	return nonCanonicalError("path is percent-encoded too many times")
}
