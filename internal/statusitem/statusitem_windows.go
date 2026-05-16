//go:build windows

package statusitem

import (
	"context"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	smoduser32   = windows.NewLazySystemDLL("user32.dll")
	smodshell32  = windows.NewLazySystemDLL("shell32.dll")
	smodkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	sprocGetModuleHandleW   = smodkernel32.NewProc("GetModuleHandleW")
	sprocGetModuleFileNameW = smodkernel32.NewProc("GetModuleFileNameW")
	sprocRegisterClassExW   = smoduser32.NewProc("RegisterClassExW")
	sprocCreateWindowExW    = smoduser32.NewProc("CreateWindowExW")
	sprocDestroyWindow      = smoduser32.NewProc("DestroyWindow")
	sprocDefWindowProcW     = smoduser32.NewProc("DefWindowProcW")
	sprocGetMessageW        = smoduser32.NewProc("GetMessageW")
	sprocTranslateMessage   = smoduser32.NewProc("TranslateMessage")
	sprocDispatchMessageW   = smoduser32.NewProc("DispatchMessageW")
	sprocLoadIconW          = smoduser32.NewProc("LoadIconW")
	sprocPostQuitMessage    = smoduser32.NewProc("PostQuitMessage")
	sprocShellNotifyIconW   = smodshell32.NewProc("Shell_NotifyIconW")
	sprocExtractIconExW     = smodshell32.NewProc("ExtractIconExW")
)

const (
	hwndMessage = ^uintptr(2) // (HWND)-3

	nifIcon    = 0x00000002
	nifMessage = 0x00000001
	nifTip     = 0x00000004

	nimAdd    = 0x00000000
	nimDelete = 0x00000002

	wmUser         = 0x0400
	wmTrayCallback = wmUser + 1
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmDestroy      = 0x0002

	idiApplication = 32512
)

type wndclassexW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     uintptr
}

type sWin32Msg struct {
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
	ctx  context.Context
	hwnd uintptr
}

func PrepareAccessoryApp() {
}

func Install(ctx context.Context) {
	state.Lock()
	state.ctx = ctx
	state.Unlock()
	go installAndRun(ctx)
}

func installAndRun(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := sprocGetModuleHandleW.Call(0)
	classNamePtr, err := syscall.UTF16PtrFromString("DKSTTrayWnd")
	if err != nil {
		return
	}
	wndProcCb := syscall.NewCallback(wndProc)
	wcex := wndclassexW{
		cbSize:        uint32(unsafe.Sizeof(wndclassexW{})),
		lpfnWndProc:   wndProcCb,
		hInstance:     hInst,
		lpszClassName: classNamePtr,
	}
	atom, _, _ := sprocRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))
	if atom == 0 {
		return
	}

	windowNamePtr, _ := syscall.UTF16PtrFromString("")
	hwnd, _, _ := sprocCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(windowNamePtr)),
		0,
		0, 0, 0, 0,
		hwndMessage,
		0,
		hInst,
		0,
	)
	if hwnd == 0 {
		return
	}

	state.Lock()
	state.hwnd = hwnd
	state.Unlock()

	hIcon := loadExeSmallIcon()
	if hIcon == 0 {
		hIcon, _, _ = sprocLoadIconW.Call(0, uintptr(idiApplication))
	}

	nid := notifyIconDataW{
		cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifIcon | nifMessage | nifTip,
		uCallbackMessage: wmTrayCallback,
		hIcon:            hIcon,
	}
	tip, _ := syscall.UTF16FromString("DKST Text Flow")
	copy(nid.szTip[:], tip)

	sprocShellNotifyIconW.Call(uintptr(nimAdd), uintptr(unsafe.Pointer(&nid)))

	go func() {
		<-ctx.Done()
		sprocShellNotifyIconW.Call(uintptr(nimDelete), uintptr(unsafe.Pointer(&nid)))
		sprocDestroyWindow.Call(hwnd)
	}()

	var m sWin32Msg
	for {
		r, _, _ := sprocGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			return
		}
		sprocTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		sprocDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func wndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTrayCallback:
		switch uint32(lparam & 0xFFFF) {
		case wmLButtonUp, wmRButtonUp:
			openMainWindow()
		}
		return 0
	case wmDestroy:
		sprocPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := sprocDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return ret
}

func loadExeSmallIcon() uintptr {
	var buf [windows.MAX_PATH]uint16
	n, _, _ := sprocGetModuleFileNameW.Call(0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return 0
	}
	var hSmall uintptr
	sprocExtractIconExW.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		0,
		0,
		uintptr(unsafe.Pointer(&hSmall)),
		1,
	)
	return hSmall
}

func openMainWindow() {
	state.Lock()
	ctx := state.ctx
	state.Unlock()
	if ctx == nil {
		return
	}
	go func() {
		wailsruntime.WindowSetAlwaysOnTop(ctx, false)
		wailsruntime.WindowUnminimise(ctx)
		wailsruntime.WindowSetMinSize(ctx, 900, 560)
		wailsruntime.WindowSetSize(ctx, 900, 560)
		wailsruntime.WindowCenter(ctx)
		wailsruntime.WindowShow(ctx)
		wailsruntime.EventsEmit(ctx, "app:show-main")
	}()
}
