package main

import (
	"log"
	"os/exec"
)

func SudoOutput(command string) (output string, err error) {
	cmd := exec.Command("sudo", "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Command failed: %s, output: %s", command, out)
		return "", err
	}
	return string(out), nil
}
