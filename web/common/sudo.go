package common

import (
	"os/exec"
	"strings"
)

// SudoExec runs a command with sudo prefix and returns output
func SudoExec(name string, args ...string) (string, error) {
	cmdArgs := append([]string{name}, args...)
	out, err := exec.Command("sudo", cmdArgs...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// SudoOutput runs a command with sudo prefix and returns output (Output mode)
func SudoOutput(name string, args ...string) (string, error) {
	cmdArgs := append([]string{name}, args...)
	out, err := exec.Command("sudo", cmdArgs...).Output()
	return string(out), err
}

// Exec runs a command without sudo and returns combined output
func Exec(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ExecOutput runs a command without sudo and returns output
func ExecOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}
