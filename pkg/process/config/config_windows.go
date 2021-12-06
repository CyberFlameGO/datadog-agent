// +build windows

package config

import (
	"os"
	"path/filepath"

	"github.com/DataDog/datadog-agent/pkg/util/executable"
	"github.com/DataDog/datadog-agent/pkg/util/winutil"
)

// defaultSystemProbeAddress is the default address to be used for connecting to the system probe
const defaultSystemProbeAddress = "localhost:3333"

func init() {
	if pd, err := winutil.GetProgramDataDir(); err == nil {
		defaultLogFilePath = filepath.Join(pd, "logs", "process-agent.log")
	}
	if _here, err := executable.Folder(); err == nil {
		agentFilePath := filepath.Join(_here, "..", "..", "embedded", "agent.exe")
		if _, err := os.Stat(agentFilePath); err == nil {
			defaultDDAgentBin = agentFilePath
		}
	}
}
