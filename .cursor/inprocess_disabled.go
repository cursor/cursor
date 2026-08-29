//go:build !copilot_inprocess || (!darwin && !linux && !windows)

package copilot

import "errors"

const inProcessAvailable = false

func createInProcessHost(string, inProcessHostConfig) (inProcessHost, error) {
	return nil, errors.New("in-process transport unavailable")
}
