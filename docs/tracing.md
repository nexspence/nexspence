# Distributed Tracing (OpenTelemetry)

`/metrics` answers "the p99 went up"; tracing answers "this one request, at
this one moment, got stuck waiting on what". With tracing enabled, every
sampled request produces a single trace spanning:

- the **HTTP handler** (span named by the route template, so repository names
  never blow up cardinality),
- every **database query** it caused (span names like `query SELECT` —
  trimmed, never the full SQL text),
- every **blob-store operation** (`blobstore.local.put`, `blobstore.s3.get`,
  …) with `blob.key` / `blob.size_bytes` attributes — metadata only, never
  content,
- the **background jobs** that run with no HTTP request behind them — cleanup
  (`cleanup.run_all` / `cleanup.run_policy`), blob GC (`gc.compact_all`),
  blob-store migration (`blob_store_migration.run`), Nexus import
  (`nexus_migration.run`), and replication (`replication.run_rule`) — each
  with its own root span,
- and, for replication, the **receiving Nexspence instance**: the outgoing
  push carries a W3C `traceparent` header, so the target's handler, DB, and
  blob-store spans join the same trace across the process boundary.

## Configuration

```yaml
tracing:
  enabled: true
  otlp_endpoint: "localhost:4317"  # your OTLP receiver (4317 grpc, 4318 http)
  otlp_protocol: "grpc"            # or "http"
  otlp_insecure: true              # plaintext, for a collector on the same host
  sample_ratio: 0.1                # head-sample 10% of root spans
  service_name: "nexspence"
  environment: "prod"              # deployment.environment resource attribute
```

Disabled by default. Nexspence ships no trace backend — bring any OTLP
receiver (Jaeger, Grafana Tempo, an OpenTelemetry Collector in front of a
vendor), the same way you bring Prometheus for `/metrics`.

Quick local start with Jaeger:

```bash
docker run --rm -p 16686:16686 -p 4317:4317 jaegertracing/all-in-one:latest
# tracing.otlp_endpoint: "localhost:4317", otlp_insecure: true
# UI at http://localhost:16686
```

## Sampling — and error traces

`sample_ratio` is head sampling (`ParentBased(TraceIDRatioBased)`): only root
spans roll the dice, children follow their parent, and a trace arriving with a
sampled `traceparent` stays sampled — so a cross-instance replication trace is
kept or dropped as a whole.

**"Always keep error traces" cannot be done here** and Nexspence does not
pretend to: a head sampler runs when a span is created, before the handler has
produced any status, so it can never see the eventual error. If you need that
guarantee, configure [tail-based sampling](https://opentelemetry.io/docs/concepts/sampling/#tail-sampling)
in your collector — it buffers the whole trace and decides once it is
finished.

## Log correlation

Every structured log line produced inside a traced scope carries the trace's
ids as `trace_id` / `span_id` fields: the per-request summary line
(`requestLogger`) and the log statements inside the background jobs. An error
log can be jumped to its full trace, and — since `sample_ratio` means most
requests have no trace at all — the log line is also the place that tells you
whether this particular request was one of the sampled ones worth looking up.
With tracing disabled the fields are simply absent.

## What is never recorded

Span attributes carry metadata only: routes, repository names, blob keys and
sizes, policy and rule IDs. Passwords, tokens, signing keys, DSNs, and
artifact content never enter a span — the same redaction standard the API
applies to `proxy_password` and friends.

## Cross-instance replication

The dependency edge between two Nexspence instances only appears when the
pushing side samples the trace (see above) and the receiving side has tracing
enabled too. Both sides export to their own collector; correlation happens by
trace ID in the backend.
