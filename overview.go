package main

import (
	"os"
	"fmt"
)

func validateDevice(device string) error {
	if !strings.HasPrefix(device, "/dev/") {
		return fmt.Errorf("invalid device path: %s", device)
	}
	_, err := os.Stat(device)
	if err != nil {
		return fmt.Errorf("device not found: %s", device)
	}
	return nil
}
