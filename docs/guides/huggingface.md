# Hugging Face guide — hosted, proxy and group repositories

A working `huggingface_hub` setup against Nexspence: how to create the
repositories, how to publish a model, dataset or space, how to point the
client at the repository instead of huggingface.co, and how the server
answers the Hub's metadata surface.

The Hub hosts three kinds of repository, and they are **separate
namespaces**: `gpt2` as a model and `gpt2` as a dataset are two different
repositories, with two different file lists and two different commit shas.
The clients that speak to them are `huggingface_hub` (the library the
others build on), `transformers` and `diffusers`. All of them address
another host through one environment variable, `HF_ENDPOINT`.

Everything below assumes a fresh instance on `http://localhost:8081` (the
default port of the Docker Compose quick start). Replace the host
everywhere if yours differs. The examples were run with `huggingface_hub`
2.4.0 and `transformers` 5.24.1.

## 1. Create the repositories

Nexspence ships a helper that creates all three repository types with the
right payloads:

```bash
./scripts/create-huggingface-repos.sh
```

It defaults to `http://localhost:8081` — point it at your instance with
`BASE_URL` when it differs:

```bash
BASE_URL=https://nexus.example.com ./scripts/create-huggingface-repos.sh
```

That script creates `huggingface-hosted`, `huggingface-proxy` (in front of
`https://huggingface.co`) and `huggingface-group` — the three names this
guide uses. Running it a second time reports the repositories as existing
and changes nothing:

```
[WARN]  exists   huggingface/hosted — already exists, skipping
[WARN]  exists   huggingface/proxy — already exists, skipping
[WARN]  exists   huggingface/group — already exists, skipping
```

Under the hood it does three `POST` calls against the v1-compatible API.
This is the same thing by hand, and it is worth knowing because the API is
what CI scripts talk to:

```bash
# resolve the blob store id first — a repository must reference an existing one
BLOB_STORE_ID=$(curl -s -u admin:admin123 \
  http://localhost:8081/service/rest/v1/blobstores/default \
  | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')

# hosted
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/huggingface/hosted \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"huggingface-hosted\",
    \"online\": true,
    \"allowAnonymous\": false,
    \"description\": \"Hugging Face hosted repository (models, datasets, spaces)\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\"
  }"

# proxy in front of the Hub
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/huggingface/proxy \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"huggingface-proxy\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Hugging Face proxy → https://huggingface.co\",
    \"blobStoreId\": \"${BLOB_STORE_ID}\",
    \"proxyConfig\": { \"remote_url\": \"https://huggingface.co\" }
  }"

# group: one URL for both
curl -u admin:admin123 -X POST \
  http://localhost:8081/service/rest/v1/repositories/huggingface/group \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"huggingface-group\",
    \"online\": true,
    \"allowAnonymous\": true,
    \"description\": \"Hugging Face group (hosted + proxy)\",
    \"formatConfig\": { \"member_names\": [\"huggingface-hosted\", \"huggingface-proxy\"] }
  }"
```

A repository that exists already answers `409` with the name it holds:

```json
{"error":"already exists: repository \"huggingface-hosted\""}
```

Repository URLs follow one pattern:

```
http://localhost:8081/repository/<repository-name>/
```

The format id of the v1 API is `huggingface`, and it is the segment in the
path above: `repositories/huggingface/hosted`.

## 2. The URL grammar

Two surfaces live under a repository: the **metadata surface**, which the
client reads to learn what a repository holds, and the **download path**,
which serves the bytes.

The metadata surface:

```
/api/models/<repo_id>                        ← the repository document
/api/models/<repo_id>/revision/<revision>     ← the same, at a revision
/api/models/<repo_id>/tree/<revision>[/<dir>] ← the file tree
```

Datasets and spaces speak the same shapes under their own prefix:

```
/api/datasets/<repo_id>
/api/spaces/<repo_id>
```

The download path:

```
/<repo_id>/resolve/<revision>/<file>      ← a model
/datasets/<repo_id>/resolve/<revision>/<file> ← a dataset
/spaces/<repo_id>/resolve/<revision>/<file>   ← a space
```

A model download path carries **no type prefix at all**, which is why the
repo type cannot be read off a fixed segment of the URL: the literal
`/resolve/` marker is what separates the `repo_id` from the revision.

A `repo_id` is one or two segments — `namespace/name`, or a bare `name`
for a canonical repository such as `bert-base-uncased`. Three segments are
not served:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/a/b/c
```

```json
{"error":"unknown huggingface endpoint"}
```

### The repository document

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2
```

```json
{"id":"nexspence/tiny-gpt2","author":"nexspence","sha":"ba4608ff33a407a639adda6e9291414d3eabb425","lastModified":"2026-11-25T12:00:16Z","private":false,"siblings":[{"rfilename":"config.json"},{"rfilename":"model.safetensors"},{"rfilename":"tokenizer.json"}]}
```

The document names its files **relatively** — `siblings[].rfilename` — and
carries no URL at all. The client builds the download URL out of its own
`HF_ENDPOINT`, so the document stays valid when the instance moves. The
`author` field is the namespace of a `namespace/name` repository and is
absent for a canonical one.

A revision that is not named defaults to `main`, the same default the
client uses:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2/revision/main
```

```json
{"id":"nexspence/tiny-gpt2","author":"nexspence","sha":"ba4608ff33a407a639adda6e9291414d3eabb425","lastModified":"2026-11-25T12:00:16Z","private":false,"siblings":[{"rfilename":"config.json"},{"rfilename":"model.safetensors"},{"rfilename":"tokenizer.json"}]}
```

### The file tree

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2/tree/main
```

```json
[{"type":"file","oid":"2b886f0daaf09921ffda5767099f6e817eedf1b1","size":198,"path":"config.json"},{"type":"file","oid":"ad7facb2586fc6e966c004d7d1d16b024f5805ff","size":4096,"path":"model.safetensors"},{"type":"file","oid":"118a3016b2369e7054980f959b0dabe1a194fad7","size":201,"path":"tokenizer.json"}]
```

Entry paths stay repository-relative (`onnx/model.onnx`), the way the Hub
reports them. `?recursive=true` asks for the whole subtree; without it a
directory is collapsed into one `directory` entry, the shape
huggingface.co answers with. A repository that holds
`onnx/model.onnx` answers:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/subdir-demo/tree/main
```

```json
[{"type":"directory","oid":"7c1251fafbd4182db7186ea8582711ab57a1b4dc","size":0,"path":"onnx"}]
```

and with `?recursive=true`:

```json
[{"type":"file","oid":"2b886f0daaf09921ffda5767099f6e817eedf1b1","size":198,"path":"onnx/model.onnx"}]
```

The `oid` of a directory is a digest of its path — there is no git tree id
behind it.

## 3. Point the client at the repository

One environment variable moves the client from huggingface.co to your
instance. It is the **repository root**, not the server root:

```bash
export HF_ENDPOINT=http://localhost:8081/repository/huggingface-hosted
export HF_TOKEN=nxs_…        # the token of §5
```

The client then calls `<HF_ENDPOINT>/api/models/<repo_id>` and downloads
from `<HF_ENDPOINT>/<repo_id>/resolve/<revision>/<file>` — the two shapes
of §2, under the repository.

`huggingface_hub` also takes the endpoint per call, which is what a
library that has to serve several repositories at once wants:

```python
from huggingface_hub import HfApi, hf_hub_download, snapshot_download

api = HfApi(endpoint="http://localhost:8081/repository/huggingface-hosted")

api.list_repo_files("nexspence/tiny-gpt2")
# ['config.json', 'model.safetensors', 'tokenizer.json']

api.list_repo_tree("nexspence/tiny-gpt2")
# [('config.json', 198), ('model.safetensors', 4096), ('tokenizer.json', 201)]

api.model_info("nexspence/tiny-gpt2").sha
# 'ba4608ff33a407a639adda6e9291414d3eabb425'

hf_hub_download("nexspence/tiny-gpt2", "config.json")
# …/models--nexspence--tiny-gpt2/snapshots/ba4608ff…/config.json

snapshot_download("nexspence/tiny-gpt2")
# …/models--nexspence--tiny-gpt2/snapshots/ba4608ff33a407a639adda6e9291414d3eabb425
```

The snapshot directory is named after the commit sha the repository
document reported, and holds all three files:

```
config.json
model.safetensors
tokenizer.json
```

`transformers` reaches the same repository through the same variable. It
has no endpoint keyword of its own — it calls `huggingface_hub` under the
hood, and `huggingface_hub` reads `HF_ENDPOINT` when it is imported.
`diffusers` is the same: it calls `huggingface_hub` too.

```python
from transformers import AutoConfig, AutoTokenizer

cfg = AutoConfig.from_pretrained("nexspence/tiny-gpt2")
# GPT2Config model_type = gpt2 n_embd = 16

tok = AutoTokenizer.from_pretrained("nexspence/tiny-gpt2")
# GPT2Tokenizer vocab size = 2
```

### Datasets and spaces

The client selects the namespace with `repo_type`, and the URLs it builds
follow from it: the metadata surface gains the `/api/datasets/` or
`/api/spaces/` prefix, and the download path gains `/datasets/` or
`/spaces/`.

```python
api.list_repo_files("nexspence/demo-dataset", repo_type="dataset")
# ['train.csv']

api.list_repo_files("nexspence/demo-space", repo_type="space")
# ['app.py']

hf_hub_download("nexspence/demo-dataset", "train.csv", repo_type="dataset")
# …/datasets--nexspence--demo-dataset/snapshots/4bd8725c…/train.csv
```

The same three namespaces are separate repositories on the server, so the
same name in two of them is two repositories:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/datasets/tiny-gpt2
```

```json
{"error":"repository not found: tiny-gpt2"}
```

## 4. Publish a file

Publishing is a `PUT` to the path the file is downloaded from. A file is
published at one revision, and the revision is part of the path:

```bash
curl -u admin:admin123 --fail -X PUT \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/main/config.json \
  --upload-file config.json
```

```
201
```

A dataset file is published under the `/datasets/` prefix, a space file
under `/spaces/`:

```bash
curl -u admin:admin123 --fail -X PUT \
  http://localhost:8081/repository/huggingface-hosted/datasets/nexspence/demo-dataset/resolve/main/train.csv \
  --upload-file train.csv
```

The file appears in the tree on the next read, with its size and a
checksum-derived `oid`:

```json
[{"type":"file","oid":"2b886f0daaf09921ffda5767099f6e817eedf1b1","size":198,"path":"config.json"}]
```

### What the server does not implement

The `PUT` above is a **Nexspence convention, not the Hub's publish
protocol**. Uploading to huggingface.co goes through its Commit API — a
preupload call, an LFS-batch-like negotiation for large files, then an
atomic commit over git. That protocol is specific to the Hub's backend and
has no public specification to implement against, so this server implements
none of it:

- **No git history.** A hosted repository has no commit graph. The sha it
  reports is *derived from the contents* of the revision: the type, the
  `repo_id` and the revision, then every stored file's path and sha256 in
  path order, truncated to 40 hex characters. It is a digest, not a git
  object id.
- **No LFS protocol.** `huggingface.co` resolves LFS server-side before it
  answers `resolve/`, so the clients never speak LFS to download; a proxy
  repository caches those answers as plain binaries and implements nothing
  of LFS itself.
- **No Hub Commit API.** A file is published with one `PUT`, one file at a
  time. There is no preupload, no batch negotiation and no atomic commit
  over several files.
- **A synthetic `oid`.** The `oid` of a tree entry is the asset's sha256
  truncated to a blob id's width, or a digest of the path for a directory
  entry. It is stable per content and is never claimed to be a git object
  id.

Because the sha is derived from the contents, it changes when they change —
which is what makes the client's local cache honest:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2 \
  | sed -n 's/.*"sha":"\([^"]*\)".*/\1/p'
```

```
ba4608ff33a407a639adda6e9291414d3eabb425
```

Publish a different `config.json` and the sha is another one:

```bash
curl -s -u admin:admin123 -X PUT \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/main/config.json \
  --upload-file config.json

curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2 \
  | sed -n 's/.*"sha":"\([^"]*\)".*/\1/p'
```

```
26bd26897150555f0339ca039301751e743de078
```

The consequence is the honest one: a hosted repository keeps no history, so
a sha handed out **before** a re-upload stops resolving once the contents
behind it change.

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/ba4608ff33a407a639adda6e9291414d3eabb425/config.json
```

```
404
```

The tree that sha described is genuinely no longer there. Publishing the
same bytes again brings the same sha back, because the sha is a digest of
the contents and not a counter:

```
ba4608ff33a407a639adda6e9291414d3eabb425
```

## 5. Credentials

The hosted repository above is created with `"allowAnonymous": false`, so
an anonymous read is refused with `401` and a `WWW-Authenticate: Basic`
challenge:

```bash
curl -s http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2
```

```json
{"error":"authentication required"}
```

A `huggingface_hub` client without a token fails the same way:

```
HfHubHTTPError : Client error '401 Unauthorized' for url
'http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2/tree/main?recursive=true&expand=false'
```

Create an API token and hand it to the client as `HF_TOKEN`. The client
reads the variable at request time and sends it as `Authorization: Bearer
<token>` — the header the server accepts:

```bash
curl -s -u admin:admin123 -X POST \
  http://localhost:8081/api/v1/tokens \
  -H "Content-Type: application/json" \
  -d '{"name":"huggingface-guide","expiresInDays":1}'
```

```json
{
  "id": "c391a6f2-db85-4eea-acb1-db9f21ba48e6",
  "userId": "064f68b9-21b7-40a7-8a7d-1364b45ce7c6",
  "username": "admin",
  "name": "huggingface-guide",
  "expiresAt": "2026-11-26T11:46:51.046074461Z",
  "createdAt": "2026-11-25T11:46:51.046875Z",
  "token": "nxs_a160aa8b1939afca03bda6d37ec6d0c614ea618806b9b38ae1e3c81da2722c17"
}
```

The plaintext value is in the answer once. Export it:

```bash
export HF_TOKEN=nxs_a160aa8b1939afca03bda6d37ec6d0c614ea618806b9b38ae1e3c81da2722c17
```

The same token works for a publish, because a `Bearer` token and a Basic
password are two ways of naming the same account. The publish adds a file
to the repository, and with it a new sha:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $HF_TOKEN" \
  -X PUT --data-binary "hello" \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/main/README.md
```

```
201
```

A token can also be created with scopes, and a scoped token refuses the
actions it was not given:

```bash
READ_TOKEN=$(curl -s -u admin:admin123 -X POST \
  http://localhost:8081/api/v1/tokens \
  -H "Content-Type: application/json" \
  -d '{"name":"huggingface-guide-read","scopes":["read"]}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')

curl -s -w "\nHTTP %{http_code}\n" -H "Authorization: Bearer ${READ_TOKEN}" \
  -X PUT --data-binary "hello" \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/main/README.md
```

```json
{"error":"token scope does not permit this action"}
```

```
HTTP 403
```

### Who may read and who may write

Access is decided by the privilege a user's role carries. A content
selector for this format names the **repository**, not the format — the
evaluator knows `repository` and `path`, and nothing else:

```json
{
  "name": "hfguide-cs",
  "expression": "repository == \"huggingface-hosted\""
}
```

The selector, a privilege with the read action, a role that carries it and
a user in that role — the four steps are described in
[RBAC and content selectors](../security-rbac.md).

A user with a read privilege (`"actions": ["browse", "read"]`) reads the
repository and cannot publish into it:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u hfguide-reader:Reader2026! \
  http://localhost:8081/repository/huggingface-hosted/api/models/nexspence/tiny-gpt2
```

```
200
```

```bash
curl -s -w "\nHTTP %{http_code}\n" -u hfguide-reader:Reader2026! \
  -X PUT --data-binary "x" \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/main/notes.txt
```

```json
{"error":"access denied"}
```

```
HTTP 403
```

A proxy or group repository is created with `"allowAnonymous": true` and
needs no credentials.

## 6. The proxy and the group against the Hub

The proxy sits in front of `https://huggingface.co` and relays the
metadata surface:

```bash
curl -s http://localhost:8081/repository/huggingface-proxy/api/models/hf-internal-testing/tiny-random-gpt2
```

```json
{"_id":"621ffdc136468d709f17ba61","id":"hf-internal-testing/tiny-random-gpt2","private":false,"library_name":"transformers","tags":["transformers","pytorch","tf","safetensors","gpt2",…],"modelId":"hf-internal-testing/tiny-random-gpt2","author":"hf-internal-testing","sha":"71034c5d8bde858ff824298bdedc65515b97d2b9","lastModified":"2024-04-08T18:00:37.000Z","gated":false,"siblings":[{"rfilename":".gitattributes"},{"rfilename":"config.json"},{"rfilename":"merges.txt"},{"rfilename":"model.safetensors"},…],"createdAt":"2022-03-02T23:29:05.000Z",…}
```

The body is passed through untouched. Unlike terraform, whose version
documents carry absolute download URLs that have to be pointed back at the
proxy, a Hub document names its files relatively — so there is nothing in
the body to rewrite.

The file itself is fetched from upstream and **cached** as a plain
binary:

```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  http://localhost:8081/repository/huggingface-proxy/hf-internal-testing/tiny-random-gpt2/resolve/main/config.json
```

```
200
```

```bash
curl -s -u admin:admin123 \
  "http://localhost:8081/service/rest/v1/assets?repository=huggingface-proxy" \
  | grep -o '"path":"[^"]*"'
```

```
"path":"/models/hf-internal-testing/tiny-random-gpt2/resolve/main/config.json"
```

The cache lifetime follows what the revision means: a commit sha
identifies its contents permanently, so a hit is served forever, while a
branch or tag is a moving name and is revalidated on the metadata TTL.

A `HEAD` is answered from upstream rather than from the cache — the blob
cache knows our own sha256 and nothing about the upstream commit a branch
pointed at when the copy was cached:

```bash
curl -s -I http://localhost:8081/repository/huggingface-proxy/hf-internal-testing/tiny-random-gpt2/resolve/main/config.json \
  | grep -i "^etag\|^x-repo-commit"
```

```
Etag: "4ff64fe5192d88c0b5dbfc578c775c0ce05dd7d0"
X-Repo-Commit: 71034c5d8bde858ff824298bdedc65515b97d2b9
```

A cached `GET` carries our own checksum instead:

```
Etag: "9ad49cd2ff58daa38243e4eeaac13b1259786d9418e45bfc01a1fd1fe1089e77"
X-Checksum-Sha256: 9ad49cd2ff58daa38243e4eeaac13b1259786d9418e45bfc01a1fd1fe1089e77
```

Two details of the cached answer are worth naming. A cached `GET`
carries **no** `X-Repo-Commit` — the blob cache knows the sha256 of its own
copy and nothing about the upstream commit a branch pointed at when the
copy was cached; a `HEAD`, which is answered from upstream, does carry it.
And a fetch that goes to upstream for the first time relays upstream's own
`X-Repo-Commit` next to the one the proxy sets itself, so the header
appears twice:

```bash
curl -s -o /dev/null -D - \
  http://localhost:8081/repository/huggingface-proxy/hf-internal-testing/tiny-random-gpt2/resolve/main/merges.txt \
  | grep -i "^x-repo-commit"
```

```
X-Repo-Commit: 71034c5d8bde858ff824298bdedc65515b97d2b9
X-Repo-Commit: 71034c5d8bde858ff824298bdedc65515b97d2b9
```

Both carry the same value, and the client reads the first.

A proxy refuses writes with `405`:

```bash
curl -s -w "\nHTTP %{http_code}\n" -u admin:admin123 -X PUT \
  --data-binary "x" \
  http://localhost:8081/repository/huggingface-proxy/hf-internal-testing/tiny-random-gpt2/resolve/main/config.json
```

```json
{"error":"proxy repository is read-only (use a hosted repository to publish)"}
```

```
HTTP 405
```

### The group

The group merges its members' answers. Publish the same `repo_id` into the
hosted repository that the proxy holds upstream, and the merged document
carries the files of both:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-group/api/models/hf-internal-testing/tiny-random-gpt2
```

```json
{
  "author": "hf-internal-testing",
  "id": "hf-internal-testing/tiny-random-gpt2",
  "lastModified": "2026-11-25T12:00:17Z",
  "private": false,
  "sha": "cbb2adb91e8b3ecbf3fa200e2042bbd883339179",
  "siblings": [
    { "rfilename": "model.safetensors" },
    { "rfilename": ".gitattributes" },
    { "rfilename": "config.json" },
    { "rfilename": "merges.txt" },
    { "rfilename": "pytorch_model.bin" },
    { "rfilename": "special_tokens_map.json" },
    { "rfilename": "tf_model.h5" },
    { "rfilename": "tokenizer.json" },
    { "rfilename": "tokenizer_config.json" },
    { "rfilename": "vocab.json" }
  ]
}
```

The tree is merged the same way — the members' entries unioned and
deduped by path, with the first member that lists a path winning it:

```bash
curl -s -u admin:admin123 \
  http://localhost:8081/repository/huggingface-group/api/models/hf-internal-testing/tiny-random-gpt2/tree/main
```

```json
[{"type":"file","oid":"ad7facb2586fc6e966c004d7d1d16b024f5805ff","size":4096,"path":"model.safetensors"},{"type":"file","oid":"8d1395b12188a31e8a22acea211f312d818744e9","size":791,"path":".gitattributes"},{"type":"file","oid":"4ff64fe5192d88c0b5dbfc578c775c0ce05dd7d0","size":807,"path":"config.json"},…]
```

A file only the proxy holds is served by the group all the same, because
the artifact fan-out asks the members until one answers:

```bash
curl -s -o /dev/null -w "%{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/huggingface-group/hf-internal-testing/tiny-random-gpt2/resolve/main/config.json
```

```
200
```

### The sha limitation of a merged document

The merged document reports the **first contributing member's** commit sha
— `cbb2adb9…` above, the hosted member's. There is no commit that spans
two independent repositories, so there is nothing else it could report.

A client that resolves a whole snapshot by that sha — `snapshot_download`
— therefore reaches the files of that member only:

```python
snapshot_download("hf-internal-testing/tiny-random-gpt2")
# …/snapshots/cbb2adb91e8b3ecbf3fa200e2042bbd883339179
#   model.safetensors        ← the hosted member's file, and nothing else
```

Downloading by branch or tag — `hf_hub_download(..., revision="main")`,
the common case — fans out across every member as usual:

```python
hf_hub_download("hf-internal-testing/tiny-random-gpt2", "config.json", revision="main")
# …/snapshots/71034c5d8bde858ff824298bdedc65515b97d2b9/config.json   ← the proxy member's file
```

### The pagination link behind a reverse proxy

A paginated tree answers with a `Link` header naming the next page. The
proxy rewrites that link back to itself — relayed verbatim it would send
the client straight to huggingface.co, past this repository's own
credentials, rules and audit trail:

```bash
curl -s -o /dev/null -D - \
  "http://localhost:8081/repository/huggingface-proxy/api/models/hf-internal-testing/tiny-random-gpt2/tree/main?limit=1" \
  | grep -i "^link"
```

```
Link: <http://localhost:8091/repository/huggingface-proxy/api/models/hf-internal-testing/tiny-random-gpt2/tree/main?expand=false&recursive=false&limit=1&cursor=ZXlKbWFXeGxYMjVoYldVaU9pSXVaMmwwWVhSMGNtbGlkWFJsY3lJc0luUnlaV1ZmYjJsa0lqb2lNalEwWkRBM1pUVTVObU0xTkROa1lqTTJaVEF4WlRNd01qaG1OMkV6WlRZeVpUa3lOV1poTUNKOTox>; rel="next"
```

The host in that link is the instance's own `http.base_url` — an instance
configured as the quick start (`http://localhost:8081`) puts that host in
the link, and the stand these examples were taken from was configured for
`:8091`.

This one link stays **absolute**, unlike every other URL of this format.
The clients follow the target verbatim, and neither `httpx` nor Node's
`fetch` resolves a relative reference against the request URL — a relative
target fails with "Request URL is missing an 'http://' or 'https://'
protocol" and breaks `list_repo_tree` and `HfFileSystem` outright.

The absolute URL is therefore built from the server's own `http.base_url`
configuration, never from the request's `Host` header. **Behind a reverse
proxy `http.base_url` has to name the public host**, or the next page is
unreachable:

```yaml
# config.yaml
http:
  base_url: "https://nexus.example.com"
```

```yaml
# docker-compose.yml
environment:
  NEXSPENCE_HTTP_BASE_URL: "https://nexus.example.com"
```

## 7. What the server does

- **One repository, three namespaces.** Models, datasets and spaces are
  separate namespaces inside one repository. A file is stored under its
  type, so a model called `datasets/x` cannot collide with the dataset
  `x`.
- **The metadata surface is read-only.** A `POST` or `PUT` to it answers
  `405` with `{"error":"the huggingface metadata API is read-only"}`.
- **The document carries no URL.** `siblings[].rfilename` and a tree
  entry's `path` are relative; the client builds the download URL out of
  its own endpoint. The host of the request never leaks into the body.
- **The sha is a digest of the contents.** See §4. It changes when the
  contents change, and a sha handed out before a re-upload stops
  resolving.
- **A `HEAD` is answered from the asset record.** It carries the `ETag`
  (the sha256 of the content), `X-Checksum-SHA256`, the `X-Repo-Commit`
  and the `Content-Length`, without opening the blob and without counting a
  download. `huggingface_hub` probes with a `HEAD` before it downloads
  anything and refuses to continue without an `ETag` or an
  `X-Repo-Commit`.
- **A repository is bounded.** One repository contributes at most 5000
  files to a metadata or tree answer. The file lookup that serves a
  download is not capped by that listing, so a wide repository's file
  downloads even when it falls outside the listing.
- **The tree endpoint is not paginated here.** Its `recursive` parameter is
  a filter rather than a cursor, and the client's query reaches the
  members of a group unchanged.
- **Group writes.** A group forwards an upload to its first hosted member,
  so publishing through `huggingface-group` stores the file in
  `huggingface-hosted` (`201`). The group's `formatConfig` can name the
  member that receives the upload with `"writable_member"` — it has to be
  an online hosted member of the same format, or the upload answers `409`:

  ```json
  {"error":"writable_member is not an online hosted repository matching group format"}
  ```

- **No `DELETE`.** A file is removed through the assets API, not through
  the Hub protocol.

## 8. Errors and edge cases

A `repo_id` nothing was ever published under does not exist:

```bash
curl -s -w "\nHTTP %{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/api/models/nosuch/model
```

```json
{"error":"repository not found: nosuch/model"}
```

```
HTTP 404
```

A file the repository does not keep:

```bash
curl -s -w "\nHTTP %{http_code}\n" -u admin:admin123 \
  http://localhost:8081/repository/huggingface-hosted/nexspence/tiny-gpt2/resolve/main/nosuchfile.json
```

```json
{"error":"not found: nexspence/tiny-gpt2/nosuchfile.json at revision main"}
```

```
HTTP 404
```

A wrong method on a file path:

```json
{"error":"method not allowed on a huggingface file path"}
```

```
HTTP 405
```

A path that is none of the endpoints of this format:

```json
{"error":"unknown huggingface endpoint"}
```

```
HTTP 404
```

And the two access answers, from §5:

```json
{"error":"authentication required"}
```

```json
{"error":"access denied"}
```

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `401 Unauthorized` on every call | The hosted repository is not anonymous | Send an API token as `HF_TOKEN`, or set `"allowAnonymous": true` on it |
| `403 Forbidden` on a publish | The role carries a read privilege only | Attach a privilege whose `"actions"` include `write` — see [RBAC and content selectors](../security-rbac.md) |
| `403 Forbidden` on a read for a user who has a role | The content selector names a format | Name the repository instead — `repository == "huggingface-hosted"`. The evaluator knows `repository` and `path`, and an expression it does not recognise matches nothing |
| `403 Forbidden` with "token scope does not permit this action" | The token was created with scopes | Create the token without scopes, or with a scope that covers the action |
| `404` on `/api/models/<repo_id>` | Nothing was published under that `repo_id` | A `repo_id` exists only once a file is published under it; check the spelling and the namespace |
| `404` on a `resolve/` path | The file is not in the repository at that revision | The path carries the revision — a file published under `main` is not under a tag |
| `404` on a sha that was valid before | A hosted repository keeps no history | The sha changed with the contents; re-read the document for the current one |
| `405 Method Not Allowed` on an upload | You are publishing into the **proxy** | Publish into `huggingface-hosted`; a proxy is read-only |
| `405` on the metadata surface | The metadata surface is read-only | Publish with a `PUT` to the `resolve/` path |
| `404` on `/api/models/a/b/c` | A `repo_id` has one or two segments | Use `namespace/name` or a bare `name` |
| The client ignores the instance | The endpoint was set after the import | `HF_ENDPOINT` is read when `huggingface_hub` is imported; export it before the import, or pass `endpoint=` per call |
| `transformers` still reaches huggingface.co | The endpoint is not in the environment of the process | `transformers` has no endpoint keyword — it follows `HF_ENDPOINT` only |
| The next page of a tree is unreachable | `http.base_url` names the internal host | Set `NEXSPENCE_HTTP_BASE_URL` to the public host the clients use |
| `list_repo_tree` fails with "Request URL is missing an 'http://' or 'https://' protocol" | A relative pagination link was served | The link is built from `http.base_url`; it has to be absolute — check the configuration behind a reverse proxy |

## Related

- [RBAC and content selectors](../security-rbac.md)
- [Migration from Sonatype Nexus](../nexus-migration.md)
- [API specification](../api-spec.yaml)
- [Go module guide](go.md)
- [Docker registry guide](docker.md)
- [Reverse proxy notes](reverse-proxy.md)
