package copilot

import "io"

type inProcessHost interface {
	Start() error
	Writer() io.WriteCloser
	Reader() io.ReadCloser
	Dispose()
}

type inProcessHostConfig struct {
	Environment map[string]string
	Args        []string
}
