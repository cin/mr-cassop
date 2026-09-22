# Pull Request Review Rules

These rules apply to every PR that touches Go code (operator, prober, UI backend, tests).
Reviewers should flag violations; authors should fix them before requesting review or
explain in the PR why an exception applies.

## Function length

- **Functions and methods must not exceed 20 lines of code** (excluding the signature,
  closing brace, blank lines and comments).
- Split long functions into small, focused helpers with descriptive names. Each helper
  should do one thing.
- Exceptions are allowed only when splitting would hurt readability, e.g.:
  - table-driven test case tables
  - large struct/object literals (Kubernetes manifests, default configs)
  - flat `switch` statements that map one value to another
  - generated code (`zz_generated.*`, CRD manifests)
- An exception must be justified in the PR description or with a short comment
  above the function.
- Refactoring an existing long function is welcome but not required when making a
  small, unrelated change to it. Don't make an existing function longer.

## Formatting and static checks

- Code must be `gofmt`-formatted and pass `go vet` (`make fmt vet`).
- Imports are grouped: standard library, third-party, then local (`github.com/cin/mr-cassop/...`).
- No commented-out code or leftover debug logging.

## Naming

- Follow [Effective Go](https://go.dev/doc/effective_go) and
  [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
- `MixedCaps`, never underscores. Initialisms keep a consistent case (`URL`, `ID`, `DC`, `TLS`).
- Short names for short scopes (`i`, `dc`, `pod`); descriptive names for wider scopes.
- Package names are short, lowercase, singular, and don't stutter (`cql.Client`, not `cql.CQLClient`).
- Getters don't use a `Get` prefix unless mirroring an existing API (e.g. controller-runtime).

## Errors

- Never ignore an error. If discarding one is intentional, assign it to `_` and add a comment
  saying why.
- Wrap errors with context using `github.com/pkg/errors` (`errors.Wrap` / `errors.Wrapf`),
  matching the rest of the codebase. Messages are lowercase with no trailing punctuation.
- Return errors instead of logging them and continuing; log an error only where it is handled.
- Don't `panic` outside of `main`/init code or truly unrecoverable programmer errors.
- Prefer early returns to deep `if/else` nesting; keep the happy path unindented.

## Control flow and design

- Accept interfaces, return concrete types. Define interfaces where they are consumed,
  and keep them small.
- Pass `context.Context` as the first parameter of functions that do I/O or call the
  Kubernetes API; don't store contexts in structs.
- Avoid package-level mutable state; inject dependencies through the reconciler or constructors.
- Avoid naked returns and named results except where they clarify documentation.
- No magic numbers or strings. Use named constants.
- Don't duplicate logic that already exists in `controllers/`, `utils/` or `api/`. Reuse it.

## Concurrency

- Every goroutine must have a clear exit condition (context cancellation or channel close).
- Guard shared state with a mutex or channels. `go test -race` must stay clean.
- Don't copy structs that contain a `sync.Mutex`.

## Kubernetes / controller specifics

- Reconcile logic must be idempotent. Running it twice with the same state must not
  change anything or trigger spurious updates.
- Compare desired vs. actual state before issuing an `Update`, to avoid reconcile loops.
- Use `errors.IsNotFound` and similar helpers from `k8s.io/apimachinery` rather than
  string matching.
- Log through the reconciler's logger (`r.Log`) at the appropriate level. Use `Debug`
  for noisy per-reconcile details.
- CRD changes need regenerated manifests/deepcopy (`make manifests generate`) and
  webhook validation where applicable.

## Tests

- New behavior and bug fixes come with tests: unit tests next to the code, integration
  tests in `tests/integration`, e2e in `tests/e2e` when cluster behavior changes.
- Prefer table-driven tests for multiple input cases.
- Tests must be deterministic. Use `Eventually` rather than `time.Sleep`.

## Documentation

- Exported identifiers have doc comments that start with the identifier's name.
- User-facing changes (CRD fields, Helm values, behavior) update `docs/`.
- Comments explain *why*, not *what*.
