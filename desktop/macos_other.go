//go:build !darwin

package main

import "errors"

func setDockVisible(bool)   {}
func launchedAtLogin() bool { return false }
func openLoginSettings()    {}

func moveToTrash(string) error { return errors.New("not supported on this platform") }
