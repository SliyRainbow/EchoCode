# EchoCode go-webview2 patches

This directory is based on `github.com/wailsapp/go-webview2 v1.0.22` and keeps
the upstream MIT license.

EchoCode carries a small Windows embedding patch in four `pkg/edge` files:

- report initialization errors to the host instead of terminating the process;
- expose deterministic controller, WebView, and environment cleanup;
- release COM interfaces when a native browser tab closes;
- pass rasterization scale and monitor-scale flags with the correct ABI values.

The complete module is retained so Go module replacement and reproducible
builds work from a fresh clone. The three `WebView2Loader.dll` files are
upstream runtime loader assets embedded by architecture-specific Go sources.
