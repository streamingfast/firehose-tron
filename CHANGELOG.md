# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## Unreleased

### Added

- New `--providers-failback-interval` flag (default `10m`) on `fetch` and
  `fetch-evm`, matching the flag `fireeth` exposes on its poller. The endpoint
  pools use a sticky rolling strategy, so a single transient error moved polling
  to a fallback endpoint and kept it there for the lifetime of the process, even
  after the preferred endpoint recovered. The declared order is now re-preferred
  on this interval, bounding how long a transient failure keeps polling off the
  preferred endpoint. Set it to `0` for the previous behaviour.

  On `fetch-evm` the Tron pool and the EVM pool fail back independently. A pool
  holding a single endpoint never starts a ticker.

### Changed

- `firehose-core` is updated from `v1.9.11-0.20250611153121-caf831699a88` to
  `v1.17.0`, which is what exposes `rpc.Clients.Reset`, the failback operation
  the flag above builds on. The update needed no `firetron` source change.

## v0.3.0

### Added

- Every endpoint is probed for its head block before the poller starts. A
  failing endpoint is logged, and if all endpoints of a kind fail, `firetron`
  exits with the reason instead of starting a poller that can only retry
  forever. Failures carry a hint for the usual causes (a plaintext endpoint
  dialed over TLS, certificate validation, a rejected API key). A provider that
  is down at startup therefore restarts the process rather than being polled
  blindly.
- Docker images are now published for both `linux/amd64` and `linux/arm64`.

### Changed

- Block fetch failures are now logged (`WARN`) by both the Tron and the EVM
  fetcher, with the same hint the startup check gives. The block poller retries
  them forever without logging anything, so a permanently failing endpoint used
  to be indistinguishable from a hang.

  A failure that keeps repeating identically is collapsed to one `WARN` every 30
  seconds, carrying the count of occurrences it stands for; the collapsed ones
  remain visible at `DEBUG`. A first failure, a failure that changes, and a
  failure coming back after a success are always logged immediately, so a
  fallback pool quietly serving blocks through its healthy endpoint does not
  turn into a log flood.
- The deprecated `--tron-api-key` flag now rejects a value that is an unexpanded
  variable reference (`$(VAR)`, `${VAR}`). Its value is used verbatim, so such a
  value used to become the literal API key and fail as an authentication error
  inside the poller's silent retry loop. Endpoint URLs interpolate `${VAR}` and
  `$VAR` as before; anything else that ends up as a key is now caught by the
  startup check.
- The TRON protocol definitions (`github.com/streamingfast/tron-protocol` and the
  `buf.build/streamingfast/tron-protocol` Buf module) are updated to the ones
  shipped with node release **GreatVoyage-v4.8.2.1**, which `firetron` now
  supports. The upstream changes are the removal of the `google.api.http`
  grpc-gateway annotations from `api/api.proto` (TRON's "improve HTTP API
  performance" work) and the `BELOW_THAN_ME` `ReasonCode` literal spelling fix
  (`0X24` to `0x24`, same value). No message, field or enum value changed, so
  the blocks `firetron` produces are byte for byte unaffected and no re-sync is
  needed.
- The `googleapis` Buf dependency is gone from `proto/buf.lock`: it was only
  pulled in transitively by the grpc-gateway annotations that TRON removed.
- Releases are now cut entirely by CI when a `v*` tag is pushed: the binaries,
  the Docker images and the GitHub release (whose body is the matching
  `CHANGELOG.md` section) all come from the `Build, push and release (if tag)`
  workflow. Releasing no longer requires running `sfreleaser` from a developer
  machine.
- Release assets are now bare binaries named `firetron_<os>_<arch>`, replacing
  the `firehose-tron_<os>_<arch>.tar.gz` archives. They are built by cross
  compiling inside Docker.
- The `Dockerfile` default `FIRECORE_VERSION` is now `v1.16.0` (was `v1.9.8`,
  which is only published for `linux/amd64`).
- Docker images and release binaries are built with Go 1.26 (was 1.25), which is
  also the version the test workflow runs. The module still declares `go 1.25`.

### Removed

- The `.sfreleaser` configuration and the separate `docker.yml` workflow, both
  superseded by the tag driven release workflow.

## v0.2.1

> [!NOTE]
> Re-release of v0.2.0. The v0.2.0 tag published no Docker image: the image
> build stage still used Go 1.24 while the project requires Go 1.25. Only the
> CI/Docker Go version changed, the content is otherwise identical to v0.2.0,
> repeated below.

### v0.2.0

### Added

- Endpoints (`--tron-endpoints`, `--tron-evm-endpoints`) accept a per-endpoint
  API key as an `apiKey` query parameter, e.g.
  `https://provider.io?apiKey=KEY`. This makes it possible to configure two
  providers that each require a different key, enabling real fallback.
- Endpoints accept a per-endpoint `insecure=true` query parameter to skip TLS
  certificate validation.
- Endpoint values support environment variable interpolation, e.g.
  `${QUICKNODE_RPC_URL}?apiKey=${QUICKNODE_API_KEY}`. Unresolved variables fail
  at startup with the missing variable named.

### Changed

- Minimum Go version is now 1.25.
- Endpoints are redacted (API key replaced with `<redacted>`) in startup logs.

### Deprecated

- The `--tron-api-key` flag (on `fetch`, `fetch-evm`, and `test-block`). It
  still works as the default key for endpoints without their own, but using it
  now logs a warning; move the key into each endpoint URL as
  `?apiKey=${YOUR_KEY}` instead. The flag will be removed in a future release.

### Removed

- The `--plaintext` and `--insecure` flags. Use an `http://` endpoint for
  plaintext, and `?insecure=true` on an endpoint to skip TLS certificate
  validation. Both are now per-endpoint rather than global.

### Fixed

- `fetch-evm`: the EVM client ignored the configured API key in favour of a
  hardcoded value; it now uses each endpoint's key.
- Both commands accepted an empty endpoint when the flag was left at its
  default, silently building a non-functional client.
- `fetch-evm`: the nested Tron client pool inherited the EVM request's
  remaining deadline, causing healthy Tron providers to be reported as failed
  under load; it now runs with its own fetch-duration budget.
