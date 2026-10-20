package project

import (
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/windows"
)

func stdinIsTerminal() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
}

func prepareProcess(command *exec.Cmd) {}

func stopProcess(process *os.Process, force bool) {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F").Run()
}
