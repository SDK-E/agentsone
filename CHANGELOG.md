# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security

- A bad, expired or revoked bearer token now gets 401 on every path and on both listeners, the
  Unix socket and TCP, also when a valid session cookie comes with it. The daemon no longer goes
  on as an anonymous caller, as the session cookie, or as the process the socket names.

### Changed

- A request to the signed webhook that sends its own `Authorization: Bearer` header now gets 401
  unless the token is valid. Webhooks are still authenticated by their signature.
- A lowercase `bearer` scheme now counts as a bearer token, because authentication schemes are
  case-insensitive (RFC 9110).

[Unreleased]: https://github.com/SDK-E/agentsone/commits/main
