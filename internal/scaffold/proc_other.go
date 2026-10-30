//go:build !unix

package scaffold

import "os/exec"

// ownGroup is a no-op where process groups aren't available; WaitDelay
// still stops a stream that a child keeps open.
func ownGroup(*exec.Cmd) {}
