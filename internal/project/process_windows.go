package project

import (
	"os"
	"os/exec"
	"strconv"
)

func prepareProcess(command *exec.Cmd) {}

func stopProcess(process *os.Process, force bool) {
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F").Run()
}
