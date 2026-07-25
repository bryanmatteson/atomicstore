# conditional-store

[![CI](https://github.com/bryanmatteson/atomicstore/actions/workflows/ci.yml/badge.svg)](https://github.com/bryanmatteson/atomicstore/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

`conditional-store` is a typed Go object store with explicit concurrency
contracts. It provides:

- typed entities and pluggable codecs;
- conditional single-key operations using ETags;
- local filesystem and S3 backends;
- capability-gated atomic batches; and
- renewable distributed leases with monotonic fencing tokens.

Multi-key commits and lock acquisition are capability-gated. A backend that
does not declare the required contract is rejected before mutation.

```sh
go get github.com/bryanmatteson/atomicstore/store
```

## Typed storage

Entities embed `store.Entity`:

```go
type User struct {
	store.Entity
	Name string `json:"name"`
}
```

Create a typed store over a backend:

```go
storage, err := store.NewFileStorage("./data")
if err != nil {
	return err
}

users, err := store.NewStore[User](
	storage,
	"users",
	store.WithRegisteredCodec("json"),
)
if err != nil {
	return err
}
```

The object API carries metadata between reads and writes:

```go
user := users.New("user-123")
user.Set(User{
	Entity: store.Entity{ID: "user-123"},
	Name:   "Ada",
})
if err := user.Save(ctx); err != nil {
	return err
}

loaded := users.New("user-123")
if err := loaded.Load(ctx); err != nil {
	return err
}
value := loaded.Get()
```

`Save` uses `If-None-Match: *` for a new object and the loaded ETag for an
existing object. A concurrent update returns `ErrCodePreconditionFailed`.
`Force` is an explicit unconditional overwrite.

Raw storage operations accept complete URIs:

```go
uri := store.FormatLocationURI("file", "users", "user-123")
data, metadata, err := storage.Get(ctx, uri)
```

Storage instances can also be created from registered URI schemes:

```go
storage, err := store.CreateStorageFromURI(
	ctx,
	"s3://users?region=us-east-1",
)
```

## Atomicity

Atomicity is scoped by backend capability.

| Operation | Guarantee |
| --- | --- |
| Single-key `Get` | One coherent object snapshot |
| `Put(..., IfNotExists())` | Linearizable create-if-absent |
| `Put(..., IfMatch(etag))` | Linearizable compare-and-swap |
| `Delete(..., IfMatch(etag))` | Linearizable conditional delete |
| `Object.Save` | Optimistic single-key update |
| Multi-key transaction | Atomic only through `AtomicBatchStorage` |

`FileStorage` serializes each key with a cross-process advisory lock. Writes are
staged, synchronized, and published with an atomic hard link or rename.

AWS S3 supplies strongly consistent single-key reads and conditional writes.
Custom S3-compatible endpoints must establish the same behavior through the
conformance suite.

`StorageTransaction` attaches per-key conditions while staging. A commit with
more than one key requires `AtomicBatchStorage`; otherwise it returns
`ErrCodeUnsupported` before writing anything. `MockStorage` implements atomic
batches for deterministic testing. `FileStorage` and S3 do not claim cross-key
atomicity.

`WithAllowPartialCommit(true)` opts into ordered best-effort writes. It is not
an atomic transaction.

## Distributed locks

`Locker` stores renewable leases using single-key conditional operations:

```go
leaseStore, err := store.NewStore[store.LockLease](
	storage,
	"locks",
	store.WithRegisteredCodec("json"),
)
if err != nil {
	return err
}

locker := store.NewLocker(
	leaseStore,
	store.WithLockOwner("worker-1"),
	store.WithLockTTL(30*time.Second),
)

err = locker.WithLock(ctx, "job-42",
	func(ctx context.Context, handle *store.LockHandle) error {
		return updateProtectedResource(ctx, handle.FencingToken)
	},
)
```

`WithLock` acquires, renews, and releases the lease. Renewal failure cancels the
callback context and is returned to the caller.

Lease records remain after release. Their versions form a monotonic fencing
sequence. Every protected write must carry the acquired token, and the
protected resource must atomically reject a token lower than the highest token
it has accepted. This prevents a paused holder from writing after its lease has
been replaced.

Expiry does not compare wall clocks across hosts. A contender may replace a
held lease only after observing the same ETag for the stored lease duration plus
the configured grace period. Recovery after a crashed holder can therefore
take one additional lease duration.

### Backend requirements

S3 lock storage uses one bucket, region, endpoint, and key namespace. The lease
prefix must be protected from unconditional writes, deletion, lifecycle expiry,
replication repair, and unrelated administrative mutation.

File lock storage requires all contenders to use the same root and a filesystem
that preserves:

- cross-process `flock`;
- atomic hard-link creation;
- atomic same-filesystem rename; and
- file and directory synchronization.

A local filesystem result does not certify a network mount. Shared filesystems
must be tested through the actual participating hosts and mount configuration.

## Conformance

`storetest.RunDistributedLockConformance` is the backend contract. Every
contender receives a separately constructed client. The suite verifies:

- exactly one create-if-absent winner;
- exactly one ETag compare-and-swap winner;
- immediate identical readback across clients;
- one holder during initial, released, and expired-lease contention;
- monotonic fencing tokens across release and reacquisition;
- rejection of fenced holders; and
- renewal preventing takeover during active work.

The repository also runs initial-acquisition and released-lease races across
separate operating-system processes for `FileStorage`.

Run the complete local verification:

```sh
go test ./...
go test -race ./...
go vet ./...
```

Stress the filesystem lock contract:

```sh
go test ./store \
	-run 'TestFileStorageDistributedLock(Conformance|SubprocessConformance)$' \
	-count=20
```

Certify an S3 deployment:

```sh
CONDITIONAL_STORE_S3_CONFORMANCE=1 \
CONDITIONAL_STORE_S3_BUCKET=my-lock-bucket \
CONDITIONAL_STORE_S3_REGION=us-east-1 \
go test ./store -run '^TestS3DistributedLockConformance$' -v -count=1
```

Optional S3 settings are `CONDITIONAL_STORE_S3_ENDPOINT`,
`CONDITIONAL_STORE_S3_PREFIX`, and `CONDITIONAL_STORE_S3_CONTENDERS`.
Conformance retains released lease records and prints their prefix.

A skipped S3 test reports `NOT CERTIFIED`. A backend is ready for distributed
locking when the complete suite passes against the deployment topology, the
lease namespace is administratively protected, workers honor cancellation, and
the protected resource independently proves fencing-token rejection.

## Backend extensions

A backend implements `Storage`. It implements
`LinearizableConditionalStorage` only when create-if-absent, ETag
compare-and-swap, and conditional delete are linearizable for one key.

Implementing the capability is a declaration; passing
`RunDistributedLockConformance` against the deployed backend is the evidence.

Backends that can publish every operation in a batch atomically may implement
`AtomicBatchStorage`. Other backends are rejected for multi-key atomic commits.

## Errors

Storage errors have stable codes:

```go
switch {
case store.IsErrorCode(err, store.ErrCodeNotFound):
	// The key does not exist.
case store.IsErrorCode(err, store.ErrCodePreconditionFailed):
	// A conditional operation lost a race.
case store.IsErrorCode(err, store.ErrCodeLockHeld):
	// Another holder owns the lease.
case store.IsRetriable(err):
	// The operation may be retried.
}
```

## License

MIT — see [LICENSE](LICENSE).
