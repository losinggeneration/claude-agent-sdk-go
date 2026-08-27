//go:build unix

package claudeagentsdk

import (
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

// applyUser makes the CLI run as the named user, mirroring Python's
// open_process(user=...): resolve uid/gid plus supplementary groups and
// hand them to the child as its credential.
func applyUser(cmd *exec.Cmd, name string) error {
	u, err := user.Lookup(name)
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return err
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return err
	}
	var groups []uint32
	ids, err := u.GroupIds()
	if err != nil {
		return err
	}
	for _, s := range ids {
		g, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return err
		}
		groups = append(groups, uint32(g))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: groups},
	}
	return nil
}
