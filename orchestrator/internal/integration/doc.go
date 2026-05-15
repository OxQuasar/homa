// Package integration holds focused end-to-end tests that wire the full
// orchestrator (auth + proxy + static) on top of stub/fake backends — kept
// separate from per-package unit tests so a single broken test doesn't
// obscure a unit failure during development. These tests do NOT spawn real
// podman / tailscale / git processes; the stub provisioner + fakeupstream
// suffice for protocol-level verification.
package integration
