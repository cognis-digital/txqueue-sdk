# txqueue-sdk

A clean, dependency-free **Go client SDK and CLI** for a `safequeue`-style
**durable job-queue HTTP API**. It gives you a typed client
(`Enqueue` / `Dequeue` / `Ack` / `Nack` / `Stats`), context support,
configurable timeouts, retry-with-backoff on transient errors, and a `Consumer`
helper that polls the queue and dispatches messages to a handler with automatic
ack/nack.

- Module: `github.com/cognis-digital/txqueue-sdk`
- Go: **1.22+**
- Dependencies: **standard library only** (`net/http`, `encoding/json`, `context`)
- Maintainer: **Cognis Digital**
- License: **COCL 1.0**

The on-the-wire protocol is fully documented below so the same queue server can
be driven from any language.

---


<!-- cognis:example:start -->
## 🔎 Example output

**Sample result format** _(illustrative values — run on your own data for real findings):_

```
{
  "transactions": [
    {
      "id": "1234567890",
      "status": "PENDING",
      "amount": 100.99,
      "created_at": "2023-02-20T14:30:00Z"
    },
    {
      "id": "9876543210",
      "status": "COMPLETED",
      "amount": 500.01,
      "created_at": "2023-02-19T10:45:00Z"
    }
  ]
}
```

<!-- cognis:example:end -->

## Install

```sh
go get github.com/cognis-digital/txqueue-sdk@latest
```

Build the CLI:

```sh
go build -o txqueue ./cmd/txqueue
```

---

## Library usage

### Create a client

```go
import "github.com/cognis-digital/txqueue-sdk/txqueue"

client, err := txqueue.New("http://localhost:8080",
    txqueue.WithTimeout(10*time.Second),   // per-attempt HTTP timeout
    txqueue.WithMaxRetries(3),              // retries on transient (5xx/429/network) errors
    txqueue.WithBackoff(200*time.Millisecond, 5*time.Second), // base, max backoff
)
if err != nil {
    log.Fatal(err)
}
```

The `*Client` is safe for concurrent use by multiple goroutines.

### Enqueue

```go
res, err := client.Enqueue(ctx, "do-some-work", "optional-idempotency-key")
// res.ID is the message id; res.Created is false if the idempotency key matched
// an existing message (no duplicate stored).
```

### Dequeue / Ack / Nack

```go
msg, err := client.Dequeue(ctx, 30) // lease for 30 seconds
switch {
case errors.Is(err, txqueue.ErrEmptyQueue):
    // nothing available right now
case err != nil:
    // transport / server error
default:
    if process(msg) == nil {
        client.Ack(ctx, msg.ID)  // success: remove from queue
    } else {
        client.Nack(ctx, msg.ID) // failure: return for redelivery
    }
}
```

`msg.Attempts` reports how many times the message has been delivered (including
the current delivery), which is useful for poison-message handling.

### Stats

```go
st, _ := client.Stats(ctx)
fmt.Printf("pending=%d in-flight=%d acked=%d nacked=%d\n",
    st.Pending, st.InFlight, st.Acked, st.Nacked)
```

### Consumer (poll + auto ack/nack)

```go
consumer := txqueue.NewConsumer(client, func(ctx context.Context, m txqueue.Message) error {
    return doWork(m.Body) // return nil -> auto-ack; return error -> auto-nack
}, txqueue.ConsumerConfig{
    VisibilitySeconds: 30,
    PollInterval:      time.Second, // wait between polls when the queue is empty
    OnError: func(stage string, err error) {
        log.Printf("consumer %s error: %v", stage, err)
    },
})

ctx, cancel := context.WithCancel(context.Background())
defer cancel()
err := consumer.Run(ctx) // blocks until ctx is cancelled; returns ctx.Err()
```

The `Consumer` loops: dequeue → dispatch to handler → ack on success, nack on
error. Empty-queue polls wait `PollInterval`. Transient dequeue/ack/nack
failures are reported via `OnError` and do not stop the loop.

---

## Errors

- `txqueue.ErrEmptyQueue` — sentinel returned by `Dequeue` when no message is
  available. Test with `errors.Is`.
- `*txqueue.APIError` — a non-2xx server response. Carries `StatusCode`, `Op`,
  and `Body`. Its `Transient()` method reports whether the status (5xx or 429)
  is retryable. Match with `errors.As`.

The client automatically retries transient errors (network failures, HTTP 5xx,
HTTP 429) up to the configured `WithMaxRetries`, using capped exponential
backoff. 4xx responses (other than 429) are returned immediately without retry.

### Injectable clock

Backoff sleeps and the `Consumer` poll loop go through a `txqueue.Clock`
interface. Inject one with `txqueue.WithClock` to drive time deterministically
in tests (the test suite uses a fake clock so it never touches the wall clock).

---

## CLI

```
txqueue [flags] <command> [args]

Commands:
  enqueue <message>   enqueue a message (use -key for idempotency)
  dequeue             lease and print the next message (does not ack)
  ack <id>            acknowledge a message
  nack <id>           negatively acknowledge a message
  stats               print queue statistics

Flags:
  -addr string       queue base URL (default "http://localhost:8080", env TXQUEUE_ADDR)
  -timeout duration  per-attempt HTTP timeout (default 30s)
  -retries int       retries on transient errors (default 3)
  -visibility int    dequeue lease seconds (default 30)
  -key string        idempotency key for enqueue
```

Examples:

```sh
export TXQUEUE_ADDR=http://localhost:8080
txqueue enqueue "build report" -key=report-2026-06-19
txqueue stats
txqueue dequeue -visibility=60
txqueue ack m-00001
```

Successful results are printed as indented JSON; errors go to stderr with a
non-zero exit code.

---

## Wire protocol specification

The queue server speaks JSON over HTTP. All request and response bodies are
`application/json`. This section is the normative contract — implement a client
in any language against it.

### `POST /enqueue`

Request:

```json
{ "message": "string payload", "idempotency_key": "optional-string" }
```

- `message` (required): opaque payload string.
- `idempotency_key` (optional): when present, the server must deduplicate
  against any prior enqueue with the same key and not store a duplicate.

Response `200`:

```json
{ "id": "m-00001", "created": true }
```

- `id`: server-assigned message identifier.
- `created`: `true` if a new message was stored; `false` if an existing message
  matched the idempotency key (in which case `id` is that existing message's id).

### `POST /dequeue`

Request:

```json
{ "visibility_seconds": 30 }
```

- `visibility_seconds`: lease duration. While leased, the message must not be
  delivered to other consumers. If the lease expires before an ack, the server
  must make the message available again and increment its attempt count.

Response `200` when a message is available:

```json
{ "id": "m-00001", "message": "string payload", "attempts": 1 }
```

- `attempts`: number of times this message has been delivered, including this
  delivery (starts at 1).

Response `200` when the queue is empty — an object with no (or empty) `id`:

```json
{}
```

The SDK maps the empty response to `ErrEmptyQueue`.

### `POST /ack`

Request:

```json
{ "id": "m-00001" }
```

Acknowledges successful processing; the server removes the message permanently.
Response `200` (body ignored).

### `POST /nack`

Request:

```json
{ "id": "m-00001" }
```

Negatively acknowledges; the server returns the message to the queue for
redelivery (and should increment its attempt count on next delivery).
Response `200` (body ignored).

### `GET /stats`

Response `200`:

```json
{ "pending": 3, "in_flight": 1, "acked": 10, "nacked": 2 }
```

- `pending`: messages available for dequeue.
- `in_flight`: messages currently leased (dequeued, not yet acked/nacked).
- `acked`: cumulative acknowledged count.
- `nacked`: cumulative negatively-acknowledged count.

### Error responses

Any non-2xx status indicates failure. `5xx` and `429` are treated by clients as
transient and retryable with backoff; other `4xx` are permanent. The response
body, if any, is surfaced to the caller for diagnostics.

---

## Development

```sh
go build ./...
go test ./...
go vet ./...
```

CI runs `go build` and `go test` on Ubuntu (see `.github/workflows/ci.yml`).
Tests stand up a fake server with `net/http/httptest` and use an injectable
clock, so the full suite runs with no real network and no wall-clock sleeps.

---

## License

License: **COCL 1.0**. Maintained by **Cognis Digital**.
