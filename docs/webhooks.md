# Webhooks

Nexspence fires HTTP POST callbacks to registered URLs when repository events occur.

## Events

| Event | When |
|-------|------|
| `artifact.published` | A new artifact is pushed to a hosted or proxy-cached repo |
| `artifact.deleted` | An artifact is deleted |
| `repo.created` | A repository is created |
| `repo.updated` | A repository configuration is updated |
| `repo.deleted` | A repository is deleted |
| `proxy.error` | A proxy repo fails to fetch from upstream |

## Payload

All events share this JSON structure (unused fields are omitted):

```json
{
  "event": "artifact.published",
  "timestamp": "2026-04-23T10:00:00Z",
  "repository": "my-maven-repo",
  "component": {
    "group": "com.example",
    "name": "my-lib",
    "version": "1.2.3",
    "format": "maven2"
  },
  "asset": {
    "path": "/com/example/my-lib/1.2.3/my-lib-1.2.3.jar",
    "contentType": "application/java-archive",
    "size": 204800
  }
}
```

`repo.created`, `repo.updated`, and `repo.deleted` payloads only contain `event`, `timestamp`, `repository`.

## API — CRUD

```bash
BASE=http://localhost:8081
AUTH="-u admin:admin123"

# List all webhooks
curl $AUTH $BASE/api/v1/webhooks

# Create
curl $AUTH -X POST $BASE/api/v1/webhooks \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "my-hook",
    "url": "https://example.com/hook",
    "events": ["artifact.published", "repo.created"],
    "secret": "mysecret"
  }'

# Update (replace <id>)
curl $AUTH -X PUT $BASE/api/v1/webhooks/<id> \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-hook","url":"https://example.com/hook","events":["artifact.published"],"active":false}'

# Delete
curl $AUTH -X DELETE $BASE/api/v1/webhooks/<id>

# Send test ping (synchronous — shows HTTP status from target URL)
curl $AUTH -X POST $BASE/api/v1/webhooks/<id>/test
# → {"status":200,"latency_ms":42}
```

## Headers on each delivery

| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `X-Nexspence-Event` | Event name (e.g. `artifact.published`) |
| `X-Nexspence-Signature` | `sha256=<hex>` — only present if secret is set |

## Verifying HMAC signatures (Python)

```python
import hashlib, hmac

def verify(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

## Local testing

```bash
# 1. Start the receiver
python scripts/webhook-receiver.py --port 8888 --secret mysecret

# 2. Register a webhook pointing at it
curl -u admin:admin123 -X POST http://localhost:8081/api/v1/webhooks \
  -H 'Content-Type: application/json' \
  -d '{"name":"local-test","url":"http://localhost:8888","events":["artifact.published"],"secret":"mysecret"}'

# 3. Send a test ping via UI ⚡ button, or curl:
curl -u admin:admin123 -X POST http://localhost:8081/api/v1/webhooks/<id>/test

# 4. Push any artifact — the receiver prints the full payload
```
