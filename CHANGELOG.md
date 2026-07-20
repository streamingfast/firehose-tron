# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## Unreleased

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
