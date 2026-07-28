# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## Unreleased

### Added

- Docker images are now published for both `linux/amd64` and `linux/arm64`.

### Changed

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
