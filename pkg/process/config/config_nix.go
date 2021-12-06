// +build !windows
// +build !darwin

package config

// defaultSystemProbeAddress is the default unix socket path to be used for connecting to the system probe
const defaultSystemProbeAddress = "/opt/datadog-agent/run/sysprobe.sock"
