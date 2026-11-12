# Security Policy

## Reporting a vulnerability

Please report security issues privately through GitHub's "Report a vulnerability" button on the Security tab of this repository. Do not open a public issue for a vulnerability before it has been addressed.

Include the affected version or commit, the reproduction steps and the impact you observed.

## Scope

This tool stores OAuth credentials for a chat.qwen.ai subscription on the local machine. Reports of interest include:

- the local API key being exposed to other local processes,
- credentials leaving the machine,
- the proxy binding to a non-loopback interface,
- tokens leaking into logs or HTTP responses,
- path traversal in the credential storage.

## Hardening defaults

- The server binds to `127.0.0.1` only and rejects requests whose peer address is not a loopback address.
- Inbound requests require a per-installation bearer key that is compared in constant time.
- The credential directory is created with mode `0700` and the credential file with mode `0600`.
- Access and refresh tokens never appear in logs, in `/health` or in any other response.
- The outbound `User-Agent` identifies this tool honestly; it does not impersonate the official Qwen client.
