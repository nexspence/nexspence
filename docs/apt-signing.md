# Signing an APT Repository

By default an `apt` client refuses a repository it cannot verify, which is why
an unsigned Nexspence apt repository only works from a source marked
`[trusted=yes]`. Configure a GPG key on the repository and Nexspence signs its
release metadata, so the repository can be used from a normal source line.

## Generate a key

```bash
gpg --batch --quick-generate-key "Nexspence APT <apt@example.com>" rsa4096 sign never
gpg --armor --export-secret-keys apt@example.com > apt-signing.key
```

Use `--passphrase` (or an empty one, as above) deliberately: the passphrase has
to be stored alongside the key for Nexspence to unlock it.

## Configure the repository

Set the armored private key in the repository's `formatConfig`:

```bash
curl -X PUT "$NEXSPENCE/api/v1/repositories/apt-hosted" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --rawfile key apt-signing.key '{
        formatConfig: { signing_key: $key }
      }')"
```

| Field | Meaning |
|---|---|
| `signing_key` | ASCII-armored **private** key. Empty/absent = repository stays unsigned. |
| `signing_key_passphrase` | Passphrase, when the key is protected. |

The key is stored in the repository configuration and is readable by anyone
allowed to read that configuration — treat it like any other repository
credential and give the repository a key of its own rather than reusing a
personal one.

## What clients get

| Path | Content |
|---|---|
| `/repository/<repo>/dists/<dist>/Release` | The release document. |
| `/repository/<repo>/dists/<dist>/InRelease` | The same document, clearsigned (plain when unsigned). |
| `/repository/<repo>/dists/<dist>/Release.gpg` | Detached signature; `404` when unsigned. |
| `/repository/<repo>/public.gpg` | Public half of the signing key; `404` when unsigned. |

Signatures are SHA-256; apt rejects SHA-1.

`Release` is byte-identical between requests — its `Date` follows the newest
package rather than the wall clock, because apt fetches `Release` and
`Release.gpg` separately and verifies one against the other.

## Use it

```bash
curl -fsSL "$NEXSPENCE/repository/apt-hosted/public.gpg" \
  | gpg --dearmor | sudo tee /usr/share/keyrings/nexspence.gpg > /dev/null

echo "deb [signed-by=/usr/share/keyrings/nexspence.gpg] $NEXSPENCE/repository/apt-hosted/ stable main" \
  | sudo tee /etc/apt/sources.list.d/nexspence.list

sudo apt update
```
