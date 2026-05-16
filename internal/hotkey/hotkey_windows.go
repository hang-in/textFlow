//go:build windows

package hotkey

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	moduser32   = windows.NewLazySystemDLL("user32.dll")

	procGetCurrentThreadId       = modkernel32.NewProc("GetCurrentThreadId")
	procRegisterHotKey           = moduser32.NewProc("RegisterHotKey")
	procUnregisterHotKey         = moduser32.NewProc("UnregisterHotKey")
	procGetMessageW              = moduser32.NewProc("GetMessageW")
	procPostThreadMessageW       = moduser32.NewProc("PostThreadMessageW")
	procGetForegroundWindow      = moduser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = moduser32.NewProc("GetWindowThreadProcessId")
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modWin      = 0x0008
	modNoRepeat = 0x4000

	wmHotkey = 0x0312
	wmQuit   = 0x0012

	hotkeyID = 1
)

type win32msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

var state struct {
	sync.Mutex
	handler  func(int)
	threadID uint32
	doneCh   chan struct{}
}

func Register(value string, handler func(int)) error {
	shortcut, err := Parse(value)
	if err != nil {
		return err
	}
	keyCode, ok := keyCodeFor(shortcut.Key)
	if !ok {
		return fmt.Errorf("unsupported hotkey key: %s", shortcut.Key)
	}
	mods := windowsModifiers(shortcut) | modNoRepeat

	Unregister()

	readyCh := make(chan error, 1)
	doneCh := make(chan struct{})

	state.Lock()
	state.handler = handler
	state.doneCh = doneCh
	state.Unlock()

	go runMessageLoop(keyCode, mods, readyCh, doneCh)

	if err := <-readyCh; err != nil {
		state.Lock()
		state.handler = nil
		state.doneCh = nil
		state.Unlock()
		return err
	}
	return nil
}

func Unregister() {
	state.Lock()
	tid := state.threadID
	doneCh := state.doneCh
	state.handler = nil
	state.threadID = 0
	state.doneCh = nil
	state.Unlock()

	if tid != 0 {
		procPostThreadMessageW.Call(uintptr(tid), uintptr(wmQuit), 0, 0)
	}
	if doneCh != nil {
		<-doneCh
	}
}

func runMessageLoop(keyCode, mods uint32, readyCh chan<- error, doneCh chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(doneCh)

	tid, _, _ := procGetCurrentThreadId.Call()
	state.Lock()
	state.threadID = uint32(tid)
	state.Unlock()

	r1, _, callErr := procRegisterHotKey.Call(0, hotkeyID, uintptr(mods), uintptr(keyCode))
	if r1 == 0 {
		readyCh <- fmt.Errorf("RegisterHotKey failed: %v", callErr)
		return
	}
	defer procUnregisterHotKey.Call(0, hotkeyID)

	readyCh <- nil

	var m win32msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			return
		}
		if m.message == wmHotkey {
			pid := currentForegroundPID()
			state.Lock()
			handler := state.handler
			state.Unlock()
			if handler != nil {
				go handler(int(pid))
			}
		}
	}
}

func currentForegroundPID() uint32 {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}

func windowsModifiers(shortcut Shortcut) uint32 {
	var m uint32
	if shortcut.Command {
		m |= modWin
	}
	if shortcut.Control {
		m |= modControl
	}
	if shortcut.Option {
		m |= modAlt
	}
	if shortcut.Shift {
		m |= modShift
	}
	return m
}

func keyCodeFor(key string) (uint32, bool) {
	codes := map[string]uint32{
		"A": 0x41, "B": 0x42, "C": 0x43, "D": 0x44, "E": 0x45, "F": 0x46, "G": 0x47, "H": 0x48, "I": 0x49, "J": 0x4A,
		"K": 0x4B, "L": 0x4C, "M": 0x4D, "N": 0x4E, "O": 0x4F, "P": 0x50, "Q": 0x51, "R": 0x52, "S": 0x53, "T": 0x54,
		"U": 0x55, "V": 0x56, "W": 0x57, "X": 0x58, "Y": 0x59, "Z": 0x5A,
		"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34, "5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,
		"Space": 0x20, "Tab": 0x09, "Enter": 0x0D, "Return": 0x0D, "Esc": 0x1B, "Escape": 0x1B,
		"Up": 0x26, "Down": 0x28, "Left": 0x25, "Right": 0x27,
		"-": 0xBD, "=": 0xBB, "[": 0xDB, "]": 0xDD, "\\": 0xDC, ";": 0xBA, "'": 0xDE, ",": 0xBC, ".": 0xBE, "/": 0xBF, "`": 0xC0,
	}
	code, ok := codes[key]
	return code, ok
}
