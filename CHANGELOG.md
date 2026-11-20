# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- A bad, expired or revoked bearer token now gets 401 on every path and on both listeners, the
  Unix socket and TCP, also when a valid session cookie comes with it. The daemon no longer goes
  on as an anonymous caller, as the session cookie, or as the process the socket names.
- A token issued through the API can now be revoked through the API. The daemon kept tokens under
  a different id than the one `issueAgentToken` answered, so `DELETE /v1/tokens/{token_id}` never
  reached the token and it lived until it expired, up to 30 days. `issuePanelToken` answered the
  secret value itself as the token's `id`. The id the issue answers, the id the daemon keeps and
  the id a revoke takes are now one `tok_` id, and the next call with a revoked token gets 401 on
  both listeners. The revoke's audit record names the token by that id.

### Changed

- A request to the signed webhook that sends its own `Authorization: Bearer` header now gets 401
  unless the token is valid. Webhooks are still authenticated by their signature.
- A lowercase `bearer` scheme now counts as a bearer token, because authentication schemes are
  case-insensitive (RFC 9110).
- A token value now reads `aot_tok_<ULID>_<secret>`: the part after `aot_` up to the secret is the
  token's id, the same id the issue answers and a revoke takes. The value is still opaque to
  clients.

### Removed

- Breaking: every live token of the earlier `aot_<hex>_<secret>` form is revoked when the daemon
  first starts after the upgrade, and such a value now gets 401. None of them could be revoked by
  its id. A tool outside the society that holds an agent token must issue a new one. Runners get
  a fresh token when they connect, and sign-in sessions stay signed in. Each revoked token gets
  one `token.revoke` audit record by `system:auth`, naming it by id only.

[Unreleased]: https://github.com/SDK-E/agentsone/commits/main
