//go:build !unix

package claudeagentsdk

import (
	"errors"
	"os/exec"
)

// applyUser matches Python's open_process(user=...) on non-POSIX
// platforms: unsupported, and requested explicitly rather than silently
// ignored.
func applyUser(cmd *exec.Cmd, name string) error {
	return errors.New("Options.User is not supported on this platform")
}
