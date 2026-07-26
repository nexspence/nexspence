package base

import (
	"strconv"
	"strings"
)

// CompareLooseVersions orders version strings loosely: dot/dash-split
// segments, numeric segments compare numerically, otherwise lexicographic;
// a numeric segment beats a qualifier at the same position ("1.0" >
// "1.0-SNAPSHOT" segment-wise); the empty string sorts first. Good enough
// for latest/release selection across formats; not a full semver or maven
// ComparableVersion implementation.
func CompareLooseVersions(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '-' })
	}
	as, bs := split(a), split(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aErr == nil:
			return 1
		case bErr == nil:
			return -1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	// Tail rule: a numeric extra segment extends the version upward
	// ("1.0.1" > "1.0"); a qualifier tail lowers it ("1.0-SNAPSHOT" < "1.0").
	switch {
	case len(as) < len(bs):
		if _, err := strconv.Atoi(bs[len(as)]); err != nil {
			return 1
		}
		return -1
	case len(as) > len(bs):
		if _, err := strconv.Atoi(as[len(bs)]); err != nil {
			return -1
		}
		return 1
	}
	return 0
}
