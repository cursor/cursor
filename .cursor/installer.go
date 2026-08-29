package embeddedcli

import "github.com/github/copilot-sdk/go/internal/embeddedcli"

// Config defines the inputs used to install and locate the embedded Copilot CLI.
//
// Cli and CliHash are required. If Dir is empty, the CLI is installed into the
// system cache directory. When Version is set, the CLI and runtime library are
// installed into a version-specific child directory so multiple versions can
// coexist. Linux musl alternatives, when provided, are selected automatically.
// License, when provided, is written next to the installed binary.
type Config = embeddedcli.Config

// Setup sets the embedded GitHub Copilot CLI install configuration.
// The CLI will be lazily installed when needed.
func Setup(cfg Config) {
	embeddedcli.Setup(cfg)
}

// Path returns the absolute path to the embedded Copilot CLI, installing it on
// first call if necessary. It returns an empty string when no embedded CLI was
// configured via Setup (e.g. a build compiled without the embedded runtime).
// The result is computed once and cached for the life of the process.
func Path() string {
	return embeddedcli.Path()
}
