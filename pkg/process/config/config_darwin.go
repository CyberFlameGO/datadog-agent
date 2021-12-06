package config

import (
	"fmt"
	"path/filepath"
)

// defaultSystemProbeAddress is the default unix socket path to be used for connecting to the system probe
const defaultSystemProbeAddress = "/opt/datadog-agent/run/sysprobe.sock"

// ValidateSysprobeSocket validates that the sysprobe socket config option is of the correct format.
func ValidateSysprobeSocket(sockPath string) error {
	if !filepath.IsAbs(sockPath) {
		return fmt.Errorf("socket path must be an absolute file path")
	}
	return nil
}
