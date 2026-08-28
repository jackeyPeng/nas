package common

import (
	"os"
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

// SafeWriteFile writes content to a file safely using a temp file + sudo mv.
// Avoids shell injection issues with echo/tee patterns.
func SafeWriteFile(destPath, content string) error {
	tmpFile, err := os.CreateTemp("", "nas-panel-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()
	// cp to destination via sudo, preserving permissions
	_, err = SudoExec("cp", tmpFile.Name(), destPath)
	return err
}

// SafeAppendFile appends content to a file safely using a temp file.
// Avoids shell injection issues with echo/tee -a patterns.
func SafeAppendFile(destPath, content string) error {
	// Read existing content
	existing, _ := SudoOutput("cat", destPath)
	// Write combined content
	return SafeWriteFile(destPath, existing+content)
}

// ExecFirstLine runs a command and returns its first line of output, trimmed
func ExecFirstLine(name string, args ...string) string {
	out, err := ExecOutput(name, args...)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}
