package npm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

const searchPath = "/-/v1/search"

// Computed search answers have their own bound; they are never cached or rewritten.
const maxSearchBytes = 32 << 20

type searchDocument struct {
	Objects []map[string]any `json:"objects"`
	Total   int              `json:"total"`
	Time    string           `json:"time"`
}

type searchPage struct{ size, from int }

func parseSearchPage(q url.Values) (searchPage, error) {
	p := searchPage{size: 20}
	for _, item := range []struct {
		key    string
		target *int
	}{{"size", &p.size}, {"from", &p.from}} {
		if raw, ok := q[item.key]; ok {
			n, err := strconv.Atoi(raw[0])
			if err != nil || n < 0 {
				return p, fmt.Errorf("%s must be a non-negative integer", item.key)
			}
			*item.target = n
		}
	}
	p.size = min(p.size, 250)
	return p, nil
}

func pageSearch(doc searchDocument, p searchPage) searchDocument {
	doc.Total = len(doc.Objects)
	if p.from >= len(doc.Objects) {
		doc.Objects = []map[string]any{}
		return doc
	}
	doc.Objects = doc.Objects[p.from:]
	doc.Objects = doc.Objects[:min(p.size, len(doc.Objects))]
	return doc
}

func (h *Handler) serveSearch(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		c.Header("Allow", "GET, HEAD")
		c.Status(http.StatusMethodNotAllowed)
		return
	}
	p, err := parseSearchPage(c.Request.URL.Query())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	repo, err := h.deps.Repos.Get(c.Request.Context(), c.Param("repoName"))
	if err != nil || repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "repository not found"})
		return
	}
	if repo.Type == domain.TypeProxy {
		h.serveProxySearch(c, repo, p)
		return
	}
	doc, err := h.hostedSearch(c, repo, c.Query("text"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}
	if !c.GetBool(formats.GroupMemberKey) {
		doc, err = h.filterSearch(c, repo, doc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "access check failed"})
			return
		}
		doc = pageSearch(doc, p)
	}
	if c.Request.Method == http.MethodHead && !c.GetBool(formats.GroupMemberKey) {
		c.Header("Content-Type", "application/json")
		c.Status(http.StatusOK)
		return
	}
	c.JSON(http.StatusOK, doc)
}

func (h *Handler) hostedSearch(c *gin.Context, repo *domain.Repository, text string) (searchDocument, error) {
	byName := map[string][]domain.Component{}
	for offset := 0; ; offset += 1000 {
		page, err := h.deps.Components.List(c.Request.Context(), repo.Name, 1000, offset)
		if err != nil {
			return searchDocument{}, err
		}
		for _, comp := range page.Items {
			if comp.Version == sentinelVersion || (comp.Format != "" && comp.Format != "npm") {
				continue
			}
			byName[comp.Name] = append(byName[comp.Name], comp)
		}
		if page.ContinuationToken == nil {
			break
		}
	}
	doc := searchDocument{Objects: []map[string]any{}, Time: time.Now().UTC().Format(time.RFC3339Nano)}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		comps := byName[name]
		latest := distTagsOf(comps)["latest"]
		for _, comp := range comps {
			if comp.Version != latest {
				continue
			}
			manifest := manifestOf(comp)
			pkg := map[string]any{"name": name, "version": comp.Version, "date": comp.CreatedAt.UTC().Format(time.RFC3339), "maintainers": searchMaintainers(manifest["maintainers"])}
			for _, key := range []string{"description", "keywords", "license", "author"} {
				if value, ok := manifest[key]; ok {
					pkg[key] = value
				}
			}
			if searchMatches(pkg, text) {
				doc.Objects = append(doc.Objects, map[string]any{"package": pkg, "score": map[string]any{"final": 0, "detail": map[string]any{"quality": 0, "popularity": 0, "maintenance": 0}}, "searchScore": 0})
			}
			break
		}
	}
	return doc, nil
}

func searchMatches(pkg map[string]any, text string) bool {
	values := []string{}
	for _, key := range []string{"name", "description", "keywords"} {
		switch value := pkg[key].(type) {
		case string:
			values = append(values, value)
		case []string:
			values = append(values, value...)
		case []any:
			for _, item := range value {
				if word, ok := item.(string); ok {
					values = append(values, word)
				}
			}
		}
	}
	haystack := strings.ToLower(strings.Join(values, " "))
	for _, term := range strings.Fields(strings.ToLower(text)) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

// Search is computed per query and must never become a cached artifact.
func (h *Handler) serveProxySearch(c *gin.Context, repo *domain.Repository, page searchPage) {
	query := c.Request.URL.RawQuery
	all := c.GetBool(formats.GroupMemberKey)
	if !all && h.deps.RBAC != nil {
		roles, _ := c.Get("roles")
		roleNames, _ := roles.([]string)
		allowed, err := h.deps.RBAC.CanAccessRepo(c.Request.Context(), c.GetString("userID"), roleNames, repo, "/", "read")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "access check failed"})
			return
		}
		all = !allowed
	}
	combined := searchDocument{Objects: []map[string]any{}}
	offset := 0
	bufferedBytes := 0
	for {
		if all {
			q := c.Request.URL.Query()
			q.Set("size", "250")
			q.Set("from", strconv.Itoa(offset))
			query = q.Encode()
		}
		doc, raw, status, err := fetchSearch(c, repo, query)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if status < 200 || status >= 300 {
			c.Data(status, "application/json", raw)
			return
		}
		if !all {
			if c.Request.Method == http.MethodHead && !c.GetBool(formats.GroupMemberKey) {
				c.Header("Content-Type", "application/json")
				c.Status(http.StatusOK)
				return
			}
			c.Data(http.StatusOK, "application/json", raw)
			return
		}
		bufferedBytes += len(raw)
		if bufferedBytes > maxSearchBytes || doc.Total > 10000 || len(combined.Objects)+len(doc.Objects) > 10000 {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream search is too large to merge; narrow the text query"})
			return
		}
		combined.Objects = append(combined.Objects, doc.Objects...)
		combined.Time = doc.Time
		offset += len(doc.Objects)
		if offset >= doc.Total {
			break
		}
		if len(doc.Objects) == 0 {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream search pagination made no progress"})
			return
		}
	}
	combined.Total = len(combined.Objects)
	if !c.GetBool(formats.GroupMemberKey) {
		var err error
		combined, err = h.filterSearch(c, repo, combined)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "access check failed"})
			return
		}
		combined = pageSearch(combined, page)
	}
	if c.Request.Method == http.MethodHead && !c.GetBool(formats.GroupMemberKey) {
		c.Header("Content-Type", "application/json")
		c.Status(http.StatusOK)
		return
	}
	c.JSON(http.StatusOK, combined)
}

func fetchSearch(c *gin.Context, repo *domain.Repository, query string) (searchDocument, []byte, int, error) {
	resp, err := repoproxy.FetchUpstreamOnce(c.Request.Context(), repo, searchPath, query, http.Header{"Accept": []string{"application/json"}})
	if err != nil {
		return searchDocument{}, nil, 0, fmt.Errorf("upstream search failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchBytes+1))
	if err != nil || len(body) > maxSearchBytes {
		return searchDocument{}, nil, 0, fmt.Errorf("upstream search response exceeds the read limit or could not be read")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return searchDocument{}, body, resp.StatusCode, nil
	}
	var wire struct {
		Objects []map[string]any `json:"objects"`
		Total   *int             `json:"total"`
		Time    string           `json:"time"`
	}
	if json.Unmarshal(body, &wire) != nil || wire.Objects == nil || wire.Total == nil || *wire.Total < 0 {
		return searchDocument{}, nil, 0, fmt.Errorf("invalid upstream search response")
	}
	doc := searchDocument{Objects: wire.Objects, Total: *wire.Total, Time: wire.Time}
	q, _ := url.ParseQuery(query)
	from, _ := strconv.Atoi(q.Get("from"))
	if len(doc.Objects) > 0 && (from > doc.Total || len(doc.Objects) > doc.Total-from) {
		return doc, nil, 0, fmt.Errorf("inconsistent upstream search pagination")
	}
	for _, object := range doc.Objects {
		if !validSearchName(searchObjectName(object)) {
			return doc, nil, 0, fmt.Errorf("invalid upstream search package name")
		}
	}
	return doc, body, resp.StatusCode, nil
}

func mergeSearch(parts []formats.GroupIndexPart) ([]byte, string, error) {
	doc := searchDocument{Objects: []map[string]any{}, Time: time.Now().UTC().Format(time.RFC3339Nano)}
	seen := map[string]bool{}
	for _, part := range parts {
		var member searchDocument
		if json.Unmarshal(part.Body, &member) != nil || member.Objects == nil {
			return nil, "", fmt.Errorf("member %q returned an invalid search response", part.Member)
		}
		for _, object := range member.Objects {
			pkg, _ := object["package"].(map[string]any)
			name, _ := pkg["name"].(string)
			if name == "" {
				return nil, "", fmt.Errorf("search result has no package name")
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			doc.Objects = append(doc.Objects, object)
		}
	}
	sort.SliceStable(doc.Objects, func(i, j int) bool { return searchObjectName(doc.Objects[i]) < searchObjectName(doc.Objects[j]) })
	doc.Total = len(doc.Objects)
	body, err := json.Marshal(doc)
	return body, "application/json", err
}

func searchObjectName(object map[string]any) string {
	pkg, _ := object["package"].(map[string]any)
	name, _ := pkg["name"].(string)
	return name
}

// GroupIndexMemberQuery asks for all contributions before the group pages them.
func (h *Handler) GroupIndexMemberQuery(p, raw string) string {
	if normPath(p) != searchPath {
		return raw
	}
	q, _ := url.ParseQuery(raw)
	q.Set("size", "250")
	q.Set("from", "0")
	return q.Encode()
}

// PageGroupIndex applies paging after merging and deduplicating search results.
func (h *Handler) PageGroupIndex(c *gin.Context, p string, body []byte) ([]byte, error) {
	if normPath(p) != searchPath {
		return body, nil
	}
	page, err := parseSearchPage(c.Request.URL.Query())
	if err != nil {
		return nil, err
	}
	var doc searchDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	repo, err := h.deps.Repos.Get(c.Request.Context(), c.Param("repoName"))
	if err != nil || repo == nil {
		return nil, fmt.Errorf("repository lookup failed")
	}
	doc, err = h.filterSearch(c, repo, doc)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pageSearch(doc, page))
}

func validSearchName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\%?#") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) > 2 || (len(parts) == 2 && (!strings.HasPrefix(parts[0], "@") || len(parts[0]) == 1)) {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (h *Handler) filterSearch(c *gin.Context, repo *domain.Repository, doc searchDocument) (searchDocument, error) {
	if h.deps.RBAC == nil {
		return doc, nil
	}
	roles, _ := c.Get("roles")
	roleNames, _ := roles.([]string)
	visible := make([]map[string]any, 0, len(doc.Objects))
	for _, object := range doc.Objects {
		name := searchObjectName(object)
		if !validSearchName(name) {
			return doc, fmt.Errorf("invalid search package name")
		}
		allowed, err := h.deps.RBAC.CanAccessRepo(c.Request.Context(), c.GetString("userID"), roleNames, repo, "/"+name, "read")
		if err != nil {
			return doc, err
		}
		if allowed {
			visible = append(visible, object)
		}
	}
	doc.Objects = visible
	doc.Total = len(visible)
	return doc, nil
}

// ValidateGroupIndexRequest runs before a group replaces member paging values.
func (h *Handler) ValidateGroupIndexRequest(c *gin.Context, p string) error {
	if normPath(p) != searchPath {
		return nil
	}
	_, err := parseSearchPage(c.Request.URL.Query())
	return err
}

// GroupIndexMemberFailureIsFatal prevents a failed search from becoming a
// partial list, or a merge/filter error from falling back to unfiltered data.
func (h *Handler) GroupIndexMemberFailureIsFatal(p string, status int) bool {
	return normPath(p) == searchPath && status != http.StatusNotFound
}

// GroupIndexMergeFailureIsFatal keeps search filtering fail-closed while
// preserving the existing best-effort packument merge policy.
func (h *Handler) GroupIndexMergeFailureIsFatal(p string) bool {
	return normPath(p) == searchPath
}

// npm search calls maintainers.map even for --json output. A missing or
// malformed package.json field must therefore still produce an array.
func searchMaintainers(value any) []map[string]string {
	result := []map[string]string{}
	raw, err := json.Marshal(value)
	if err != nil {
		return result
	}
	var people []struct{ Username, Name, Email string }
	if json.Unmarshal(raw, &people) != nil {
		return result
	}
	for _, person := range people {
		username := person.Username
		if username == "" {
			username = person.Name
		}
		if username == "" {
			continue
		}
		item := map[string]string{"username": username}
		if person.Email != "" {
			item["email"] = person.Email
		}
		result = append(result, item)
	}
	return result
}
