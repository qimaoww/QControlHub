//go:build linux

package agent

import (
	"os/exec"
	"syscall"
	"time"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
}

func configureCommandIdentity(command *exec.Cmd, identity *commandIdentity) {
	if command == nil || identity == nil {
		return
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Credential = &syscall.Credential{
		Uid: identity.uid, Gid: identity.gid, Groups: identity.groups,
	}
}
