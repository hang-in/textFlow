//go:build windows

package platform

/*
#cgo CFLAGS: -DCINTERFACE -DCOBJMACROS
#cgo LDFLAGS: -lole32 -loleaut32

#include <stdlib.h>

char* DKSTGetSelectedTextUTF8(void);
void DKSTFreeText(char* p);
*/
import "C"

import (
	"errors"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	seluser32   = windows.NewLazySystemDLL("user32.dll")
	selkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	selProcSendInput        = seluser32.NewProc("SendInput")
	selProcOpenClipboard    = seluser32.NewProc("OpenClipboard")
	selProcCloseClipboard   = seluser32.NewProc("CloseClipboard")
	selProcEmptyClipboard   = seluser32.NewProc("EmptyClipboard")
	selProcGetClipboardData = seluser32.NewProc("GetClipboardData")
	selProcSetClipboardData = seluser32.NewProc("SetClipboardData")

	selProcGlobalAlloc  = selkernel32.NewProc("GlobalAlloc")
	selProcGlobalLock   = selkernel32.NewProc("GlobalLock")
	selProcGlobalUnlock = selkernel32.NewProc("GlobalUnlock")
	selProcGlobalFree   = selkernel32.NewProc("GlobalFree")
	selProcLstrlenW     = selkernel32.NewProc("lstrlenW")
)

const (
	selCfUnicodeText = 13
	selGmemMoveable  = 0x0002

	selVkControl = 0x11
	selVkV       = 0x56
	selVkC       = 0x43

	selInputKeyboard    = 1
	selKeyeventfKeyup   = 0x0002
	selKeyeventfUnicode = 0x0004
)

type selKeybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

type selInput struct {
	Type uint32
	_    [4]byte
	Ki   selKeybdInput
	_    [8]byte
}

func selectedText() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cstr := C.DKSTGetSelectedTextUTF8()
	if cstr == nil {
		return "", errors.New("no UI Automation text selection available")
	}
	defer C.DKSTFreeText(cstr)
	text := C.GoString(cstr)
	if text == "" {
		return "", nil
	}
	return text, nil
}

func selectedTextFromProcess(processID int) (string, error) {
	return selectedText()
}

func replaceSelectedTextInProcess(processID int, replacement string, preferPaste bool) error {
	if processID > 0 {
		_ = activateProcess(processID)
		time.Sleep(40 * time.Millisecond)
	}

	backup, hadBackup := selReadClipboardText()
	if !selSetClipboardText(replacement) {
		return errors.New("failed to set clipboard for replacement")
	}
	selSendCtrlV()
	time.Sleep(120 * time.Millisecond)
	if hadBackup {
		selSetClipboardText(backup)
	}
	return nil
}

func selSendCtrlV() {
	inputs := []selInput{
		{Type: selInputKeyboard, Ki: selKeybdInput{Vk: selVkControl}},
		{Type: selInputKeyboard, Ki: selKeybdInput{Vk: selVkV}},
		{Type: selInputKeyboard, Ki: selKeybdInput{Vk: selVkV, Flags: selKeyeventfKeyup}},
		{Type: selInputKeyboard, Ki: selKeybdInput{Vk: selVkControl, Flags: selKeyeventfKeyup}},
	}
	selSendInputBatch(inputs)
}

func selSendInputBatch(inputs []selInput) {
	if len(inputs) == 0 {
		return
	}
	selProcSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(selInput{}))
}

func selReadClipboardText() (string, bool) {
	r, _, _ := selProcOpenClipboard.Call(0)
	if r == 0 {
		return "", false
	}
	defer selProcCloseClipboard.Call()
	h, _, _ := selProcGetClipboardData.Call(uintptr(selCfUnicodeText))
	if h == 0 {
		return "", false
	}
	ptr, _, _ := selProcGlobalLock.Call(h)
	if ptr == 0 {
		return "", false
	}
	defer selProcGlobalUnlock.Call(h)
	length, _, _ := selProcLstrlenW.Call(ptr)
	if length == 0 {
		return "", true
	}
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), int(length))
	return syscall.UTF16ToString(buf), true
}

func selSetClipboardText(text string) bool {
	utf16 := syscall.StringToUTF16(text)
	if len(utf16) == 0 {
		utf16 = []uint16{0}
	}
	size := uintptr(len(utf16) * 2)
	hMem, _, _ := selProcGlobalAlloc.Call(uintptr(selGmemMoveable), size)
	if hMem == 0 {
		return false
	}
	ptr, _, _ := selProcGlobalLock.Call(hMem)
	if ptr == 0 {
		selProcGlobalFree.Call(hMem)
		return false
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(utf16))
	copy(dst, utf16)
	selProcGlobalUnlock.Call(hMem)

	r, _, _ := selProcOpenClipboard.Call(0)
	if r == 0 {
		selProcGlobalFree.Call(hMem)
		return false
	}
	selProcEmptyClipboard.Call()
	set, _, _ := selProcSetClipboardData.Call(uintptr(selCfUnicodeText), hMem)
	selProcCloseClipboard.Call()
	if set == 0 {
		selProcGlobalFree.Call(hMem)
		return false
	}
	return true
}
