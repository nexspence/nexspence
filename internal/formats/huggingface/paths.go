package huggingface

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
)

// The three kinds of repository the Hub hosts. They are separate namespaces:
// "gpt2" as a model and "gpt2" as a dataset are different repositories, so the
// type is part of every coordinate and every stored path.
const (
	typeModels   = "models"
	typeDatasets = "datasets"
	typeSpaces   = "spaces"
)

// defaultRevision is the revision a client that names none is asking for —
// huggingface_hub's own default.
const defaultRevision = "main"

// api request kinds.
const (
	kindInfo = "info"
	kindTree = "tree"
)

// repoRef is a parsed Hugging Face repository reference: a type plus a repo_id,
// which is "namespace/name" or a bare "name" for a canonical repo such as
// bert-base-uncased.
type repoRef struct {
	Type      string
	Namespace string // "" for a canonical repo
	Name      string
}

// id returns the repo_id as the client spells it.
func (r repoRef) id() string {
	if r.Namespace == "" {
		return r.Name
	}
	return r.Namespace + "/" + r.Name
}

// group returns the component group these coordinates live under: the repo type
// for a canonical repo, type/namespace otherwise. Same shape as terraform's
// "<ns>/<name>" module group — a group that is a path, not a single token.
func (r repoRef) group() string {
	if r.Namespace == "" {
		return r.Type
	}
	return r.Type + "/" + r.Namespace
}

func (r repoRef) coords(revision string) base.Coords {
	return base.Coords{Group: r.group(), Name: r.Name, Version: revision}
}

// storePrefix is the asset path every file of this repository lives under.
//
// It always carries the repo type, including for models — whose own URLs don't
// (huggingface.co serves a model at /<repo_id>/resolve/... and a dataset at
// /datasets/<repo_id>/resolve/...). Storing all three the same way keeps the
// layout self-describing in the browse tree and keeps a model called
// "datasets/x" from colliding with the dataset "x". Proxy mode passes the
// client's original path as the UPSTREAM path, so the difference stays local.
func (r repoRef) storePrefix() string {
	return "/" + r.Type + "/" + r.id()
}

// revisionPrefix is the asset path prefix of one revision's files.
func (r repoRef) revisionPrefix(revision string) string {
	return r.storePrefix() + "/resolve/" + revision + "/"
}

// storePath is the asset path of one file at one revision.
func (r repoRef) storePath(revision, filename string) string {
	return r.revisionPrefix(revision) + filename
}

// parseRepoID splits a repo_id into namespace and name. A repo_id is one or two
// segments — the Hub has no deeper nesting — so a third segment means the caller
// located the wrong marker and the path is not one we serve.
func parseRepoID(repoType, raw string) (repoRef, bool) {
	id := strings.Trim(raw, "/")
	if id == "" {
		return repoRef{}, false
	}
	segs := strings.Split(id, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return repoRef{}, false
		}
	}
	switch len(segs) {
	case 1:
		return repoRef{Type: repoType, Name: segs[0]}, true
	case 2:
		return repoRef{Type: repoType, Namespace: segs[0], Name: segs[1]}, true
	default:
		return repoRef{}, false
	}
}

// splitTypePrefix strips the /datasets/ or /spaces/ prefix a non-model download
// path carries, and reports which type the path names. A model path has no
// prefix at all, which is why the type cannot be read off a fixed segment.
func splitTypePrefix(p string) (repoType, rest string) {
	switch {
	case strings.HasPrefix(p, "/datasets/"):
		return typeDatasets, strings.TrimPrefix(p, "/datasets")
	case strings.HasPrefix(p, "/spaces/"):
		return typeSpaces, strings.TrimPrefix(p, "/spaces")
	default:
		return typeModels, p
	}
}

// parseResolve parses a download/publish path:
//
//	/<repo_id>/resolve/<revision>/<filename>            (models)
//	/datasets/<repo_id>/resolve/<revision>/<filename>   (datasets)
//	/spaces/<repo_id>/resolve/<revision>/<filename>     (spaces)
//
// A repo_id is one or two segments, so the path cannot be split by counting
// them: the literal "/resolve/" marker is what separates the repo_id from the
// revision — the same trick terraform's handler uses to find "/upload/" and
// "/download/" in a variable-length path. (A repository whose own name is
// "resolve" is ambiguous under this grammar, on huggingface.co exactly as much
// as here, and is not resolved specially.)
func parseResolve(p string) (ref repoRef, revision, filename string, ok bool) {
	repoType, rest := splitTypePrefix(p)
	idx := strings.Index(rest, "/resolve/")
	if idx <= 0 {
		return repoRef{}, "", "", false
	}
	ref, ok = parseRepoID(repoType, rest[:idx])
	if !ok {
		return repoRef{}, "", "", false
	}
	tail := strings.TrimPrefix(rest[idx+len("/resolve/"):], "/")
	revision, filename, _ = strings.Cut(tail, "/")
	if revision == "" || filename == "" {
		return repoRef{}, "", "", false
	}
	return ref, revision, filename, true
}

// parseAPIPath parses the metadata surface:
//
//	/api/{models|datasets|spaces}/<repo_id>
//	/api/{models|datasets|spaces}/<repo_id>/revision/<revision>
//	/api/{models|datasets|spaces}/<repo_id>/tree/<revision>[/<subpath>]
//
// Same variable-length repo_id problem as parseResolve, solved the same way:
// the "/tree/" and "/revision/" markers, not segment counting.
func parseAPIPath(p string) (ref repoRef, kind, revision, subpath string, ok bool) {
	rest, found := strings.CutPrefix(p, "/api/")
	if !found {
		return repoRef{}, "", "", "", false
	}
	repoType, body, _ := strings.Cut(rest, "/")
	switch repoType {
	case typeModels, typeDatasets, typeSpaces:
	default:
		return repoRef{}, "", "", "", false
	}
	body = "/" + strings.Trim(body, "/")

	if i := strings.Index(body, "/tree/"); i > 0 {
		if ref, ok = parseRepoID(repoType, body[:i]); !ok {
			return repoRef{}, "", "", "", false
		}
		tail := strings.TrimPrefix(body[i+len("/tree/"):], "/")
		revision, subpath, _ = strings.Cut(tail, "/")
		if revision == "" {
			return repoRef{}, "", "", "", false
		}
		return ref, kindTree, revision, strings.Trim(subpath, "/"), true
	}
	if i := strings.Index(body, "/revision/"); i > 0 {
		if ref, ok = parseRepoID(repoType, body[:i]); !ok {
			return repoRef{}, "", "", "", false
		}
		revision = strings.Trim(body[i+len("/revision/"):], "/")
		if revision == "" {
			return repoRef{}, "", "", "", false
		}
		return ref, kindInfo, revision, "", true
	}
	if ref, ok = parseRepoID(repoType, body); !ok {
		return repoRef{}, "", "", "", false
	}
	return ref, kindInfo, defaultRevision, "", true
}

// isCommitSHA reports whether a revision names a commit rather than a branch or
// tag. A commit sha identifies its contents for good; a branch name does not,
// which is the whole difference proxy caching turns on.
func isCommitSHA(revision string) bool {
	if len(revision) != 40 {
		return false
	}
	for i := 0; i < len(revision); i++ {
		c := revision[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// revisionCommit derives the commit sha a hosted revision reports.
//
// A hosted repository has no git graph, so there is no real commit to name —
// but huggingface_hub requires one and does not treat it as cosmetic: it labels
// the local cache directory of every downloaded file with the X-Repo-Commit the
// server returned, and snapshot_download resolves a whole snapshot under the
// sha that repo_info reported. Both answers therefore have to agree, and the
// value has to change when the revision's contents change — otherwise a client
// that cached an earlier download keeps serving it from
// snapshots/<sha>/<file> after a re-upload, having never asked again.
//
// So it is derived from the revision's own contents: the type, repo_id and
// revision, then every stored file's path and sha256 in path order. A revision
// that already IS a commit sha is reported as itself.
func revisionCommit(ref repoRef, revision string, files []domain.Asset) string {
	if isCommitSHA(revision) {
		return strings.ToLower(revision)
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s/%s@%s\n", ref.Type, ref.id(), revision)
	for _, f := range files {
		fmt.Fprintf(h, "%s %s\n", f.Path, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))[:40]
}

// blobOID is the "oid" a tree entry carries. huggingface_hub's RepoFile reads
// that field unconditionally, so an entry without one is a KeyError in the
// client rather than a missing detail — but a real git blob id (sha1 over a
// git object header plus the contents) is not something this server has. The
// asset's own sha256, truncated to a blob id's width, stands in for it: stable
// per content, and never claimed to be a git object id.
func blobOID(a domain.Asset) string {
	if len(a.SHA256) >= 40 {
		return a.SHA256[:40]
	}
	return pathOID(a.Path)
}

// pathOID is blobOID for a tree entry with no contents of its own — a
// directory, whose real git tree id this server has no way to compute either.
func pathOID(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])[:40]
}

// contentTypeFor derives a Content-Type for a repository file.
//
// The extension wins over whatever the client declared, which is the opposite
// of raw's order and deliberate: nothing in the Hub's publish flow carries a
// meaningful content type. A raw-body upload has no media type of its own to
// declare, so an HTTP client fills the header with its own default — a plain
// `curl -X PUT --data-binary` sends application/x-www-form-urlencoded — and
// trusting that stores the label and serves it back on every later download.
// A repository of model weights served as form encoding was exactly what a
// live publish produced before this order was fixed.
//
// The declared type is consulted only for an extension Go's own table does not
// know AND a header that says something a raw body could actually be. The Hub's
// characteristic payloads (.safetensors, .onnx, .bin, .parquet, .gguf) are all
// in the first group, so they fall through to octet-stream — which is what
// huggingface.co itself serves them as.
func contentTypeFor(filename, declared string) string {
	if ct := mime.TypeByExtension(path.Ext(filename)); ct != "" {
		return ct
	}
	switch declared {
	case "", "application/x-www-form-urlencoded", "application/octet-stream":
		return "application/octet-stream"
	default:
		return declared
	}
}

func normPath(p string) string {
	return path.Clean("/" + strings.TrimPrefix(p, "/"))
}
