//go:build !windows && !darwin

package main

import "errors"

func appLoginStatus() (bool, string) { return false, "not supported on this platform" }
func setAppLogin(bool) error         { return errors.New("not supported on this platform") }
