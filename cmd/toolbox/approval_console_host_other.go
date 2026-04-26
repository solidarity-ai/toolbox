//go:build !darwin && !linux

package main

func approvalConsoleClientHostForPID(int) approvalConsoleClientHost {
	return approvalConsoleClientHost{}
}
