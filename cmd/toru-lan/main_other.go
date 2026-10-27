//go:build !windows

package main

import (
	"fmt"
	"os"
)

func run(args []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(version)
		return 0
	}
	fmt.Fprintln(os.Stderr, "toru-lan runs on Windows only")
	return 1
}
