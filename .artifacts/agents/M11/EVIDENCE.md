# Public acceptance record: M11

This is a source-release summary. Original local execution artifacts remain private because they may include personal garden data. Current GitHub CI reproduces the verification gates.

| 2026-11-06 | M11 | ACCEPTED | Client-host stdio proxy and original-photo helper; race-enabled real child-process protocol tests pass. Hermes/Grok native account checks explicitly unverified. Evidence: `.artifacts/agents/M11/client-tests.log`. |

# M11 — Client packages and onboarding

Status: ACCEPTED (client package; native accounts unverified).

Implemented the pinned-SDK stdio proxy, secure same-origin bearer HTTP client and original-photo upload helper on the agent host. Added Hermes HTTP/stdio and xAI remote-tool examples with explicit local/hosted reachability and Grok Bot account boundaries. No external account or public tunnel was configured.

Evidence: `.artifacts/agents/M11/client-tests.log`: race-enabled tests pass, including actual stdio child-process handshake/discovery, image forwarding, revoked credential calls, endpoint/redirect checks and original Android bytes/stable retry identities. Hermes and Grok Bot executables/accounts are unavailable and remain explicitly unverified; examples are not claimed as native-client setup proof. See `examples/agents/README.md`.
