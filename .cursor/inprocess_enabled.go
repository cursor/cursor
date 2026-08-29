//go:build copilot_inprocess && (darwin || linux || windows)

package copilot

import "github.com/github/copilot-sdk/go/internal/ffihost"

const inProcessAvailable = true

func createInProcessHost(runtimePath string, config inProcessHostConfig) (inProcessHost, error) {
	return ffihost.Create(runtimePath, config.Environment, config.Args)
}
