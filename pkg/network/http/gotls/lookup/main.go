package luts

// This generates each lookup table's implementation in ./luts.go:
// - Use /var/tmp/datadog-agent/system-probe/go-toolchains
//   as the location for the Go toolchains to be downloaded to.
//   Each toolchain version is around 500 MiB on disk.
// TODO: enable arm64 once machine code scanning works
//go:generate go run ./internal/generate_luts.go --test-program ./internal/program.go --package luts --out ./luts.go --min-go 1.15 --arch amd64 --max-quick-go 1.17.3 --shared-build-dir /var/tmp/datadog-agent/system-probe/go-toolchains
