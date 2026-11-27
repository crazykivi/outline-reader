//go:build !windows

package main

func validateDataDirACL(string) error {
	return nil
}
