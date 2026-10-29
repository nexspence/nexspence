package apt

// Suites (#727): one repository, one shared pool, a Packages/Release per
// dists/<suite>/. A deb belongs to the suites recorded on its component — one
// component per (Package, Version, Architecture), and one file per identity —
// so a file shared by several release lines is stored once.
//
// A deb uploaded without a suite has no record and belongs to every suite, so
// a repository that never names one serves the same index under any
// dists/<name>/, as before. Naming a suite for such a file records it, and
// from then on the file is listed under its recorded suites only.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

const (
	// debSuitesKey holds a component's suites, a sorted list of names. Absent
	// means every suite; an empty list means none.
	debSuitesKey = "apt_suites"
	// debSuitesChangedKey holds when the suites last changed (RFC 3339). Every
	// suite's Release Date follows the latest such change in the repository,
	// so a suite a deb joined or left never keeps a Date apt would take for
	// the one it already has.
	debSuitesChangedKey = "apt_suites_changed"
	// debSuitesByKey holds who wrote the record: an upload's token (which
	// names its path), when the record is the one that upload's component
	// started with, or suitesByChange when a join or leave wrote it. A new
	// file restates its suites over the record of an upload to another path,
	// never over a change or an upload to its own path (restateSuites).
	debSuitesByKey = "apt_suites_by"
	suitesByChange = "change"
	// maxSuitesPerRequest bounds the suite parameters of one request.
	maxSuitesPerRequest = 32
)

// suiteNamePattern accepts Debian suite and codename spellings ("stable",
// "bookworm-backports", "acme-1.5.1", "1.0~rc1"). It cannot contain "/" or a
// line break, so a suite is always one dists/ path segment and one line of a
// Release document.
var suiteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]{0,99}$`)

// errSuiteName is a suite name suiteNamePattern refuses.
var errSuiteName = errors.New("invalid suite name: letters, digits and . _ + ~ - only, starting with a letter or digit, at most 100 characters")

// requestSuites reads the request's "suite" parameters, sorted and deduplicated.
func requestSuites(values []string) ([]string, error) {
	if len(values) > maxSuitesPerRequest {
		return nil, fmt.Errorf("at most %d suite parameters per request", maxSuitesPerRequest)
	}
	for _, v := range values {
		if !suiteNamePattern.MatchString(v) {
			return nil, fmt.Errorf("%w: %q", errSuiteName, v)
		}
	}
	return normalizeSuites(values), nil
}

// normalizeSuites sorts and deduplicates; nil stays nil (no record).
func normalizeSuites(suites []string) []string {
	if suites == nil {
		return nil
	}
	out := append([]string{}, suites...)
	sort.Strings(out)
	return slices.Compact(out)
}

// componentSuites is the suites recorded on comp; recorded is false when
// none were, and the deb then belongs to every suite. Stored lists come back
// from JSONB as []any.
func componentSuites(comp *domain.Component) (suites []string, recorded bool) {
	if comp == nil {
		return nil, false
	}
	raw, ok := comp.Extra[debSuitesKey]
	if !ok || raw == nil {
		return nil, false
	}
	switch v := raw.(type) {
	case []string:
		return v, true
	case []any:
		for _, s := range v {
			if str, ok := s.(string); ok {
				suites = append(suites, str)
			}
		}
		return suites, true
	}
	return nil, true // unreadable record: listed nowhere rather than everywhere
}

// inSuite reports whether the deb filed under comp is listed in dists/<suite>/.
func inSuite(comp *domain.Component, suite string) bool {
	suites, recorded := componentSuites(comp)
	return !recorded || slices.Contains(suites, suite)
}

// suitesChanged is when comp's suites last changed; zero when never.
func suitesChanged(comp *domain.Component) time.Time {
	if comp == nil {
		return time.Time{}
	}
	s, _ := comp.Extra[debSuitesChangedKey].(string)
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// suitesExtra is the extra that records suites, written by by, stamped now.
func suitesExtra(suites []string, by string) map[string]any {
	if suites == nil {
		suites = []string{} // recorded, and empty: listed in no suite
	}
	return map[string]any{
		debSuitesKey:        suites,
		debSuitesChangedKey: time.Now().UTC().Format(time.RFC3339Nano),
		debSuitesByKey:      by,
	}
}

// suitesBy is who wrote comp's suites record; empty when unknown.
func suitesBy(comp *domain.Component) string {
	if comp == nil {
		return ""
	}
	by, _ := comp.Extra[debSuitesByKey].(string)
	return by
}

// newUploadToken names one upload to storePath in the records it writes.
func newUploadToken(storePath string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "upload:" + hex.EncodeToString(b[:]) + ":" + storePath
}

// tokenPath is the path an upload token names; empty for anything else.
func tokenPath(token string) string {
	parts := strings.SplitN(token, ":", 3)
	if len(parts) != 3 || parts[0] != "upload" {
		return ""
	}
	return parts[2]
}

// uploadExtra is what a stored upload's component starts with (base.Coords
// Extra): its control paragraph and, when it names any, its suites. They are
// then on the component before the file can be listed, so a new deb is never
// served without its stanza, or in a suite it was not uploaded to. A
// redeploy keeps the suites the file is in (keptExtra), and adds the named
// ones afterwards.
func uploadExtra(ctl *debControl, suites []string, token string) map[string]any {
	extra := map[string]any{debSuitesByKey: token}
	if ctl != nil {
		extra[debControlKey] = ctl.Paragraph
	}
	if suites != nil {
		for k, v := range suitesExtra(normalizeSuites(suites), token) {
			extra[k] = v
		}
	}
	return extra
}

// keptExtra are the extra keys a redeploy with other content keeps from the
// component: suite membership is where the file is listed, not what it is.
// The upsert keeps them itself, so a suite change made while the body
// streamed is not undone. They are domain.ListingExtraKeys, which promotion
// leaves behind for the same reason.
var keptExtra = domain.ListingExtraKeys

// withSuitesLock serializes the read-modify-write of one component's suites,
// in this process and across instances. Nothing is streamed while it is
// held, so a database lock is cheap here.
func (h *Handler) withSuitesLock(ctx context.Context, componentID string, fn func(ctx context.Context) error) error {
	key := "apt-suites:" + componentID
	defer base.LockLocal(key)()
	return h.deps.Assets.WithBlobKeyLock(ctx, key, fn)
}

// recordSuites adds suites to a component's recorded ones (with no record
// yet, the component takes add alone) and stamps the change. It writes
// nothing when the suites stay as they are, so a repeated upload leaves the
// Release unchanged.
func (h *Handler) recordSuites(ctx context.Context, componentID string, add []string) error {
	return h.withSuitesLock(ctx, componentID, func(ctx context.Context) error {
		comp, err := h.deps.Components.Get(ctx, componentID)
		if err != nil {
			return fmt.Errorf("look up the package to record its suites: %w", err)
		}
		cur, recorded := componentSuites(comp)
		next := normalizeSuites(append(slices.Clone(cur), add...))
		if recorded && slices.Equal(next, cur) {
			return nil
		}
		return h.deps.Components.UpdateExtra(ctx, componentID, suitesExtra(next, suitesByChange))
	})
}

// restateSuites makes sure a new file is listed where its upload (token) said,
// once stored: another instance's losing first upload of the same identity,
// to another path, may have reset the component to its own record before
// giving way. Only such a record is replaced — with exactly suites, nil
// meaning "no record", every suite. A join or leave that landed since the
// file was stored, or an upload to this same path on another instance, is
// another client's acknowledged change: it stays, and the named suites are
// added to it.
func (h *Handler) restateSuites(ctx context.Context, componentID string, suites []string, token string) error {
	want := normalizeSuites(suites)
	return h.withSuitesLock(ctx, componentID, func(ctx context.Context) error {
		comp, err := h.deps.Components.Get(ctx, componentID)
		if err != nil {
			return fmt.Errorf("look up the package to record its suites: %w", err)
		}
		cur, _ := componentSuites(comp)
		by := suitesBy(comp)
		switch {
		case by == token:
			return nil // the record this upload stored with the file
		case by == suitesByChange || tokenPath(by) == tokenPath(token):
			next := normalizeSuites(append(slices.Clone(cur), want...))
			if want == nil || slices.Equal(next, cur) {
				return nil
			}
			return h.deps.Components.UpdateExtra(ctx, componentID, suitesExtra(next, suitesByChange))
		}
		extra := suitesExtra(want, token)
		if want == nil {
			extra[debSuitesKey] = nil // JSON null: no record
		}
		return h.deps.Components.UpdateExtra(ctx, componentID, extra)
	})
}

// spooledBody is the start of an upload body copied to a temporary file, so
// its digest can be compared with the stored file's before anything is
// stored, and it can then still be stored.
type spooledBody struct {
	f      *os.File
	size   int64
	sha256 string
}

// spoolBody copies at most limit bytes of body.
func spoolBody(body io.Reader, limit int64) (*spooledBody, error) {
	f, err := os.CreateTemp("", "nexspence-apt-*.deb")
	if err != nil {
		return nil, err
	}
	_ = os.Remove(f.Name()) // unlinked: the open handle keeps it until Close
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(body, limit))
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &spooledBody{f: f, size: n, sha256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (s *spooledBody) Close() { _ = s.f.Close() }

// errJoinedSuites ends an upload that added suites to the file already stored
// at its path with the same content, instead of storing it again.
var errJoinedSuites = errors.New("joined suites")

// joinStoredDeb handles an upload naming suites for an identity already
// stored. At another path it is refused (409), as any second file for an
// identity: suites are added through the file's own path, which is what the
// uploader's permissions were checked against. At its path with the same
// content, the file joins the suites, nothing is stored, and errJoinedSuites
// ends the upload with 200. With other content it is a redeploy, left to the
// caller and the write policy: rest is then the body to store, and spooled
// (when not nil) the temporary file to close after. Only as many bytes as the
// stored file has are read ahead, and none when the declared size differs,
// so the quota and the write policy still judge the rest. Callers hold
// withDebLocks.
func (h *Handler) joinStoredDeb(ctx context.Context, repo *domain.Repository, storePath string, coords base.Coords, arch string,
	body io.Reader, size int64, suites []string, ctl *debControl,
) (rest io.Reader, spooled *spooledBody, status int, err error) {
	holders, err := h.identityHolders(ctx, repo.Name, coords, arch)
	if err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	if len(holders) == 0 {
		return body, nil, 0, nil
	}
	holder := holders[0]
	if holder.Path != storePath {
		return nil, nil, http.StatusConflict, fmt.Errorf("%s %s (%s) is already stored at %s; to add it to a suite, upload it to that path",
			coords.Name, coords.Version, arch, holder.Path)
	}
	if size > 0 && size != holder.SizeBytes {
		return body, nil, 0, nil // other content: a redeploy
	}
	spooled, err = spoolBody(body, holder.SizeBytes+1)
	if err != nil {
		return nil, nil, http.StatusInternalServerError, fmt.Errorf("read the upload: %w", err)
	}
	if spooled.size != holder.SizeBytes || spooled.sha256 != holder.SHA256 {
		return io.MultiReader(spooled.f, body), spooled, 0, nil
	}
	spooled.Close()

	componentID := holder.ComponentID
	if comp, err := h.deps.Components.Get(ctx, componentID); err == nil && comp != nil && comp.Group == "" && ctl != nil {
		// A pre-#637 deb: its component may hold other architectures, whose
		// suites would change with it. The upload has the same bytes, so its
		// control paragraph is this file's: file it under its own component
		// first, as a redeploy would.
		if componentID, err = h.fileUnderControl(ctx, repo, holder.Path, coords, ctl.Paragraph, comp.ID); err != nil {
			return nil, nil, http.StatusInternalServerError, err
		}
	}
	if err := h.recordSuites(ctx, componentID, suites); err != nil {
		return nil, nil, http.StatusInternalServerError, err
	}
	return nil, nil, http.StatusOK, errJoinedSuites
}

// joinIfStored runs joinStoredDeb for an upload that names suites; see there.
// A body without a control file cannot name one: there is no architecture to
// file it under, and its component would be shared with every other
// architecture of its version.
func (h *Handler) joinIfStored(ctx context.Context, repo *domain.Repository, storePath string, coords base.Coords, arch string,
	ctl *debControl, body io.Reader, size int64, suites []string,
) (io.Reader, *spooledBody, int, error) {
	if len(suites) == 0 || repo == nil {
		return body, nil, 0, nil
	}
	if ctl == nil {
		return nil, nil, http.StatusBadRequest, errors.New("a suite needs a Debian package with a control file")
	}
	return h.joinStoredDeb(ctx, repo, storePath, coords, arch, body, size, suites, ctl)
}

// pathHeld reports whether a file is stored at p.
func (h *Handler) pathHeld(ctx context.Context, repoName, p string) (bool, error) {
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, p)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && asset == nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up %s: %w", p, err)
	}
	return true, nil
}

// settleSuites records an upload's suites once it is stored. A new file
// states exactly the named ones (restateSuites); a redeploy kept the suites
// the file was in and adds the named ones.
func (h *Handler) settleSuites(ctx context.Context, componentID string, newFile bool, suites []string, token string) error {
	if newFile {
		return h.restateSuites(ctx, componentID, suites, token)
	}
	if len(suites) == 0 {
		return nil
	}
	return h.recordSuites(ctx, componentID, suites)
}

// fileUnderControl moves the pre-#637 deb at p from legacyID to the component
// its control paragraph names, recording the paragraph there.
func (h *Handler) fileUnderControl(ctx context.Context, repo *domain.Repository, p string, coords base.Coords,
	control, legacyID string,
) (string, error) {
	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name, Format: string(domain.FormatApt),
		Group: coords.Group, Name: coords.Name, Version: coords.Version,
	}
	if err := h.deps.Components.Create(ctx, comp); err != nil {
		return "", fmt.Errorf("component for %s: %w", p, err)
	}
	if err := h.deps.Components.UpdateExtra(ctx, comp.ID, map[string]any{debControlKey: control}); err != nil {
		return "", err
	}
	return comp.ID, h.migrateLegacyAsset(ctx, repo.Name, p, legacyID, comp.ID)
}

// uploadSuites reads an upload's suites; ok is false once it has answered the
// request. A suite change writes no file, so the store's own check would never
// see it: it is refused on a read-only repository up front.
func uploadSuites(c *gin.Context, repo *domain.Repository) (suites []string, ok bool) {
	suites, err := requestSuites(c.QueryArray("suite"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if len(suites) > 0 && repo != nil {
		if err := base.CheckWritable(repo); err != nil {
			c.JSON(base.HTTPStatusForError(err), gin.H{"error": err.Error()})
			return nil, false
		}
	}
	return suites, true
}

// serveLeaveSuite answers DELETE /pool/...?suite=<name>.
func (h *Handler) serveLeaveSuite(c *gin.Context, repo *domain.Repository, p string) {
	suites, err := requestSuites(c.QueryArray("suite"))
	if err == nil && len(suites) != 1 {
		err = errors.New("name one suite to leave")
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if status, err := h.leaveSuite(c.Request.Context(), repo, p, suites[0]); err != nil {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// leaveSuite removes the deb at p from suite; the file stays stored. It takes
// the path lock an upload to p takes, then the component's suites lock.
func (h *Handler) leaveSuite(ctx context.Context, repo *domain.Repository, p, suite string) (int, error) {
	if err := base.CheckWritable(repo); err != nil {
		return base.HTTPStatusForError(err), err
	}
	defer base.LockLocal(debPathKey(repo.Name, p))()
	asset, err := h.deps.Assets.GetByPath(ctx, repo.Name, p)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && asset == nil) {
		return http.StatusNotFound, fmt.Errorf("%s not found", p)
	}
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("look up %s: %w", p, err)
	}
	status := http.StatusNoContent
	err = h.withSuitesLock(ctx, asset.ComponentID, func(ctx context.Context) error {
		comp, err := h.deps.Components.Get(ctx, asset.ComponentID)
		if err != nil || comp == nil {
			status = http.StatusInternalServerError
			return fmt.Errorf("look up the package at %s: %w", p, err)
		}
		suites, recorded := componentSuites(comp)
		if !recorded {
			status = http.StatusConflict
			return fmt.Errorf("%s was uploaded without a suite and is listed in every suite; upload it again naming the suites it belongs to", p)
		}
		if !slices.Contains(suites, suite) {
			status = http.StatusNotFound
			return fmt.Errorf("%s is not in suite %s", p, suite)
		}
		left := slices.DeleteFunc(slices.Clone(suites), func(s string) bool { return s == suite })
		if err := h.deps.Components.UpdateExtra(ctx, comp.ID, suitesExtra(left, suitesByChange)); err != nil {
			status = http.StatusInternalServerError
			return err
		}
		return nil
	})
	return status, err
}
