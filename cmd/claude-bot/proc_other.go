//go:build !unix

package main

import (
	"errors"
	"os/exec"
)

func lockFile(string) (func(), error) {
	return nil, errors.New("claude-bot serve is only supported on macOS and Linux")
}

func detach(*exec.Cmd) {}

func terminate(int) error {
	return errors.New("claude-bot serve is only supported on macOS and Linux")
}
