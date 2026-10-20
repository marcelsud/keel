//go:build !windows

package project

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func stdinIsTerminal() bool {
	_, err := unix.IoctlGetWinsize(int(os.Stdin.Fd()), unix.TIOCGWINSZ)
	return err == nil
}

func prepareProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopProcess(process *os.Process, force bool) {
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-process.Pid, signal)
}
