# Security policy

## Reporting a vulnerability

Please report it privately with GitHub's
[private vulnerability reporting](https://github.com/tarakanof/ember/security/advisories/new)
(the repository's **Security** tab › **Report a vulnerability**). Don't open a
public issue for it. Include what you found, how to reproduce it and the
version (`GET /version`). You will get an answer as soon as the maintainer can
give one; this is a spare-time project, so there is no fixed deadline.

## In scope

- The server's authentication: the master `EMBER_TOKEN`, scoped client tokens
  (`ekc_…`) and their scopes, and the unauthenticated routes.
- Token handling and storage, in the server, the app and the producers.
- The device protocol between the server and the cinder knob, including pairing.
- Anything that lets a network peer without a token change settings or read secrets.

## Out of scope

- Ember is built for a trusted home LAN. Exposing it to the internet without a
  reverse proxy and TLS is not supported.
- Vulnerabilities in the AWTRIX NG firmware, macOS, Docker or other third-party
  software: report those upstream.
- Findings that need an attacker who already holds the master token or a
  logged-in session on your Mac.
