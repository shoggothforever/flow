# Sanitized Flow source package

This archive contains the Flow source corresponding to source revision `a0f562a`.

The package intentionally excludes Git history, `flow.config.json`, runtime configuration and session data, build outputs, logs, editor state, local Agent/Codex/AWS directories, and credential or private-key files. Machine-specific paths in examples and development guidance were replaced with generic paths.

Build and install with:

```bash
make VERSION=a0f562a COMMIT=a0f562a install
```

The active configuration is created separately under `~/.config/flow/config.json`.
