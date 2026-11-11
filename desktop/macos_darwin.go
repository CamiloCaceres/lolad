//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework ServiceManagement
#include <stdlib.h>
#include "macos_darwin.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

// setDockVisible switches between a regular app (Dock icon, ⌘-Tab) and a menu
// bar accessory. Wails has no API for this after launch.
func setDockVisible(v bool) {
	if v {
		C.lolad_set_dock_visible(1)
	} else {
		C.lolad_set_dock_visible(0)
	}
}

func launchedAtLogin() bool { return C.lolad_launched_at_login() == 1 }

func cErr(msg *C.char) error {
	defer C.free(unsafe.Pointer(msg))
	return errors.New(C.GoString(msg))
}

// appLoginStatus reports whether the app opens at login. Detail is a short
// human line when it is not simply on or off.
func appLoginStatus() (enabled bool, detail string) {
	switch C.lolad_login_status() {
	case 1:
		return true, ""
	case 2:
		return false, "waiting for approval in System Settings"
	case 3:
		return false, "only works from the installed app"
	case -1:
		return false, "needs macOS 13 or newer"
	}
	return false, ""
}

func setAppLogin(on bool) error {
	var msg *C.char
	v := C.int(0)
	if on {
		v = 1
	}
	if C.lolad_login_set(v, &msg) != 0 {
		return cErr(msg)
	}
	return nil
}

func openLoginSettings() { C.lolad_open_login_settings() }

func moveToTrash(path string) error {
	cp := C.CString(path)
	defer C.free(unsafe.Pointer(cp))
	var msg *C.char
	if C.lolad_trash(cp, &msg) != 0 {
		return cErr(msg)
	}
	return nil
}
