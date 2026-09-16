package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func setQuota(volumePath string, quotaGB int) error {
	fsType, err := getFileSystemType(volumePath)
	if err != nil {
		return err
	}

	if fsType != "xfs" {
		return fmt.Errorf("file system %s does not support project quota", fsType)
	}

	// 设置配额的逻辑
	// ...
	return nil
}

func getFileSystemType(path string) (string, error) {
	cmd := exec.Command("df", "-T", path)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("failed to get file system type for %s", path)
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 2 {
		return "", fmt.Errorf("failed to parse file system type for %s", path)
	}
	return fields[1], nil
}
