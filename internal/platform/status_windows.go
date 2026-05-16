//go:build windows

package platform

import (
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pmoduser32   = windows.NewLazySystemDLL("user32.dll")
	pmodkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pprocGetForegroundWindow      = pmoduser32.NewProc("GetForegroundWindow")
	pprocGetWindowThreadProcessId = pmoduser32.NewProc("GetWindowThreadProcessId")
	pprocIsWindowVisible          = pmoduser32.NewProc("IsWindowVisible")
	pprocGetWindow                = pmoduser32.NewProc("GetWindow")
	pprocEnumWindows              = pmoduser32.NewProc("EnumWindows")
	pprocAllowSetForegroundWindow = pmoduser32.NewProc("AllowSetForegroundWindow")
	pprocShowWindow               = pmoduser32.NewProc("ShowWindow")
	pprocSetForegroundWindow      = pmoduser32.NewProc("SetForegroundWindow")
	pprocIsIconic                 = pmoduser32.NewProc("IsIconic")

	pprocOpenProcess                = pmodkernel32.NewProc("OpenProcess")
	pprocCloseHandle                = pmodkernel32.NewProc("CloseHandle")
	pprocQueryFullProcessImageNameW = pmodkernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	asfwAny                        = 0xFFFFFFFF
	swRestore                      = 9
	swShow                         = 5
	gwOwner                        = 4
	processQueryLimitedInformation = 0x1000
)

func CurrentStatus() Status {
	status := Status{
		AccessibilityTrusted: true,
	}
	pid := currentForegroundPID()
	if pid != 0 {
		info := appInfoFromPID(pid)
		status.ActiveAppName = info.Name
		status.ActiveBundleID = info.BundleID
	}
	return status
}

func requestAccessibilityPermission() bool {
	return true
}

func activateProcess(processID int) error {
	if processID <= 0 {
		return errors.New("invalid process id")
	}
	hwnd := findMainWindowForPID(uint32(processID))
	if hwnd == 0 {
		return errors.New("no top-level window found for the process")
	}
	pprocAllowSetForegroundWindow.Call(uintptr(asfwAny))
	r1, _, _ := pprocIsIconic.Call(hwnd)
	if r1 != 0 {
		pprocShowWindow.Call(hwnd, uintptr(swRestore))
	} else {
		pprocShowWindow.Call(hwnd, uintptr(swShow))
	}
	r2, _, callErr := pprocSetForegroundWindow.Call(hwnd)
	if r2 == 0 {
		return errors.New("SetForegroundWindow failed: " + callErr.Error())
	}
	return nil
}

func appInfoFromProcess(processID int) AppInfo {
	if processID <= 0 {
		return AppInfo{}
	}
	return appInfoFromPID(uint32(processID))
}

func appInfoFromBundlePath(path string) AppInfo {
	path = strings.TrimSpace(path)
	if path == "" {
		return AppInfo{}
	}
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return AppInfo{
		Name:     name,
		BundleID: path,
		Path:     path,
	}
}

func currentForegroundPID() uint32 {
	hwnd, _, _ := pprocGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	pprocGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func appInfoFromPID(pid uint32) AppInfo {
	hProc, _, _ := pprocOpenProcess.Call(uintptr(processQueryLimitedInformation), 0, uintptr(pid))
	if hProc == 0 {
		return AppInfo{}
	}
	defer pprocCloseHandle.Call(hProc)

	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	r1, _, _ := pprocQueryFullProcessImageNameW.Call(hProc, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r1 == 0 {
		return AppInfo{}
	}
	path := syscall.UTF16ToString(buf[:size])
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return AppInfo{
		Name:     name,
		BundleID: path,
		Path:     path,
	}
}

func findMainWindowForPID(pid uint32) uintptr {
	var found uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, lparam uintptr) uintptr {
		var winPID uint32
		pprocGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&winPID)))
		if winPID != pid {
			return 1
		}
		visible, _, _ := pprocIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		owner, _, _ := pprocGetWindow.Call(hwnd, uintptr(gwOwner))
		if owner != 0 {
			return 1
		}
		found = hwnd
		return 0
	})
	pprocEnumWindows.Call(cb, 0)
	return found
}
