# hoplockserver

A tiny HTTP server that exposes the minimum compare-and-swap API needed to
back a [hoplock](https://github.com/xinix00/hoplock) `Backend`. Think of it
as a fake-S3 for lease files: GET/PUT/DELETE on opaque keys, with
`If-Match` and `If-None-Match` preconditions, and one shared API key for
auth. No buckets, no sigv4, no AWS account.

It exists so anyone can run hoplock-based leader election without standing
up an S3-compatible object store first.

## Run

```bash
go build -o hoplockserver ./cmd/hoplockserver

./hoplockserver \
  -listen :8090 \
  -data ./data \
  -api-key "$(openssl rand -hex 32)"
```

| Flag        | Default   | Notes                                          |
|-------------|-----------|------------------------------------------------|
| `-listen`   | `:8090`   | TCP listen address                             |
| `-data`     | `./data`  | Directory where lease files are persisted      |
| `-api-key`  | empty     | Required `X-API-Key` header; empty disables auth |

`HOPLOCK_API_KEY` is read when `-api-key` is empty.

## Use it from hoplock

```go
import (
    "github.com/xinix00/hoplock"
    hopclient "github.com/xinix00/hoplockserver/client"
)

elector := &hoplock.Elector{
    Backend: &hopclient.Backend{
        URL:    "http://lock.internal:8090",
        Key:    "hop/clusters/prod-eu",
        APIKey: os.Getenv("HOPLOCK_API_KEY"),
    },
    Owner: "node-1",
    TTL:   30 * time.Second,
}

_ = elector.Lead(ctx, func(leaderCtx context.Context, lease hoplock.Lease) error {
    return doLeaderWork(leaderCtx, lease.Generation)
})
```

## HTTP API

| Method | Path     | Preconditions                            | Notes                       |
|--------|----------|------------------------------------------|-----------------------------|
| GET    | `/<key>` | —                                        | Returns body + `ETag`       |
| PUT    | `/<key>` | `If-None-Match: *` or `If-Match: <etag>` | Body is opaque (≤1 MiB)     |
| DELETE | `/<key>` | optional `If-Match: <etag>`              | —                           |
| GET    | `/health`| —                                        | Always 200, no auth         |

ETag = `"<hex sha256 of body>"`. They are deterministic on body content,
so callers can validate a write succeeded by recomputing locally.

## Persistence and consistency

One file per key under `-data`. Writes are atomic (`write tmp + rename`).
Compare-and-swap is serialised by a single in-process mutex, so a single
hoplockserver instance is linearisable — but **don't point two
hoplockserver replicas at the same data directory**, they will race.

For HA, run a real S3-compatible store and use `hoplock/s3` instead. This
server is the small/free option.

## Status

Pre-1.0, in tandem with hoplock. Co-developed with `hop` as the
[Hop infrastructure suite](https://github.com/xinix00/hop)'s drop-in
replacement for hopraft.
