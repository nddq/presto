# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Presto, please report it
responsibly by emailing **28567936+nddq@users.noreply.github.com**. Do not open a public
issue.

You should receive an acknowledgement within 48 hours. A fix will be
developed privately and released as a patch version before public
disclosure.

## Scope

Presto is a local CLI tool and optional HTTP server. The main attack
surfaces are:

- **WAV decoder** (`internal/audio/`) -- malformed input files
- **HTTP server** (`cmd/presto/server.go`) -- uploaded WAV payloads
- **Store loader** (`internal/store/`) -- crafted `.prfp` library files

The WAV decoder is fuzz-tested (`go test ./internal/audio -fuzz=FuzzDecodeWAV`).
