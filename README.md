# primitives-go

[![Go Reference](https://pkg.go.dev/badge/github.com/primandproper/primitives-go.svg)](https://pkg.go.dev/github.com/primandproper/primitives-go)

The tier every service is built from: providers behind interfaces, the transports whose shape somebody else decided, the database and schema tooling stores are built with, and the handful of values both tiers have to agree on. Layers that touch the network — HTTP, gRPC, database, messaging — instrument with OpenTelemetry.

**Module:** `github.com/primandproper/primitives-go`
**Go:** 1.27

The module is empty today. Its packages arrive in one move from `platform-go`, with history, in primandproper/primitives-go#2; the catalog below is the shape they land in.

## What belongs here

> **primitives-go ships what every service is built from and no service is.** Four kinds of thing qualify: a provider behind an interface (`cache`, `email`, `messagequeue`, ...); a transport whose shape is decided by something other than the consumer's domain (a probe, a protocol, a middleware contract, a third party's payload); the database and schema tooling stores are built with (`database` and its subpackages, `filtering`); and the cross-cutting values both tiers have to agree on (`tenancy.Scope`, the `errors` sentinels, `clock`). Nothing in it owns a table.
>
> **[platform-go](https://github.com/primandproper/platform-go) ships what a product has**: a noun with a table, its lifecycle, its transport, its permissions and its privacy obligations. The test for a new package is whether an application with no users would still need it. If yes, it is a primitive.

The dependency runs one way. platform-go imports this module; **this module imports platform-go from nowhere, ever.** A primitive that finds it needs a domain package has found a seam to invert, not a dependency to add — the error mappers are the worked example: each domain package exports its `Mapper` and platform-go's `service` registers it, rather than `errors/http` reaching for the domain.

## Project Status & Stability

> **`main` is not a release channel.** Anything on `main` that has not been cut into a tagged release is considered under active development — alpha/beta, unstable, and unsupported. Treat it as such.

This repository follows a deliberately conservative release model:

- **Only tagged releases are supported.** If it isn't behind a version tag, it can change or break without notice, and no support or compatibility is promised for it.
- **`main` moves ahead of the latest release.** New work — including breaking changes — lands on `main` well before it is deemed release-worthy. Two facts locate you at any moment, and both are derived rather than written down here: the module path in `go.mod` is the major that `main` is currently building toward, and the highest version tag is the latest supported release. Whatever is on `main` but not yet in that tag is subject to change — and immediately after a major bump, that is the entire major.
- **Semantic Versioning, enforced by Go's module paths.** Breaking changes increment the major version and the module import path (`/vN` → `/vN+1`), so a major bump can never silently break a consumer that hasn't opted in. The path bump lands in the same change that makes the break, never as a follow-up, which is why `main`'s major is frequently one ahead of anything you can fetch by tag.
- **No stability guarantees on unreleased APIs.** Interfaces, config shapes, and package boundaries on `main` are subject to change until they ship in a release.

If you depend on this library, pin to a released tag — and note that `@latest` against a major that has no tag yet resolves to a commit on `main` rather than to a release. If you want to track upcoming work, `main` is fair game — just don't expect it to hold still.

This module is v1, and being the slow tier it intends to stay there. That is an intention rather than a promise: the model above is the promise, and a v2 would arrive the same way any major does.

## Installation

```bash
go get github.com/primandproper/primitives-go@latest
```

Because breaking changes ride the major-version import path, upgrading across majors is an explicit, opt-in edit to your import paths — never a surprise from `go get -u`.

## Package Catalog

Empty until the move (primandproper/primitives-go#2) fills it. The headings are the ones the packages sort into; each row will name a package, what it is for, and the implementations it ships, with a `noop` for most concerns.

### Data & storage

### Messaging & events

### Web & transport

### Observability & operations

### Auth & security

### AI, ML & product

### Coordination

### Utilities

## Development

```bash
make setup          # Install dev tools and download deps
make format         # Format all Go code (imports, field/tag alignment, gofmt)
make lint           # Run golangci-lint (Docker) + shellcheck
make test           # Run tests (race detector, shuffle, failfast)
make build          # Build all packages
make generate       # Regenerate moq mocks after changing a mocked interface
make proto format   # Regenerate the Go bindings for the .proto files this module ships
make bench          # Run benchmarks
```

Formatting runs locally with `gci`, `goimports`, `betteralign`, `tagalign`, and `gofmt`. Linting runs in Docker against the `golangci/golangci-lint` image (42+ linters, golangci-lint v2 format).

### Testing conventions

- **`stretchr/testify` is banned** (`assert`, `require`, and `mock`), enforced by `depguard`. Use [`shoenig/test`](https://github.com/shoenig/test) for assertions (`test` for non-fatal, `must` for fatal) and [`matryer/moq`](https://github.com/matryer/moq) for mocks.
- Tests run in parallel by default and use subtests throughout.
- Container-backed tests use `testcontainers-go`, live in-package (typically `containers_test.go`), and gate on `RUN_CONTAINER_TESTS=true`.
- `make test` runs `CGO_ENABLED=1 go test -shuffle=on -race -vet=all -failfast ./...` across every package. `.scripts/test.sh false` runs the suite without container tests.

## Contributing

Because `main` is a development channel and only tagged releases are supported, changes land on `main` freely and are stabilized before release. Follow the existing package layout (interface + config subpackage + provider implementations + `noop`), match the surrounding code, and keep `make format lint test` green.
