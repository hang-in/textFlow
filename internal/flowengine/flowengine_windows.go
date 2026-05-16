//go:build windows

package flowengine

import (
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"dkst-text-flow/internal/flow"
	"dkst-text-flow/internal/storage"
)

type Store interface {
	ListSnippets(query string) ([]storage.Snippet, error)
	LogExpansion(snippetID int64, appBundleID string) error
	LogTyping(count int64) error
}

type engine struct {
	mu            sync.Mutex
	store         Store
	onExpansion   func(storage.Snippet)
	matcher       *flow.Matcher
	running       bool
	suppressInput bool
	typingCount   int64
	typingFlush   bool
	threadID      uint32
	doneCh        chan struct{}
}

var keyboardEngine = &engine{matcher: flow.NewMatcher(96)}

var lowLevelKeyboardCallback = syscall.NewCallback(lowLevelKeyboardProc)

var (
	moduser32   = windows.NewLazySystemDLL("user32.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modimm32    = windows.NewLazySystemDLL("imm32.dll")

	procGetCurrentThreadId       = modkernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandleW         = modkernel32.NewProc("GetModuleHandleW")
	procSetWindowsHookExW        = moduser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx      = moduser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx           = moduser32.NewProc("CallNextHookEx")
	procGetMessageW              = moduser32.NewProc("GetMessageW")
	procTranslateMessage         = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW         = moduser32.NewProc("DispatchMessageW")
	procPostThreadMessageW       = moduser32.NewProc("PostThreadMessageW")
	procGetForegroundWindow      = moduser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = moduser32.NewProc("GetWindowThreadProcessId")
	procGetKeyboardLayout        = moduser32.NewProc("GetKeyboardLayout")
	procGetKeyboardState         = moduser32.NewProc("GetKeyboardState")
	procToUnicodeEx              = moduser32.NewProc("ToUnicodeEx")
	procSendInput                = moduser32.NewProc("SendInput")
	procOpenClipboard            = moduser32.NewProc("OpenClipboard")
	procCloseClipboard           = moduser32.NewProc("CloseClipboard")
	procEmptyClipboard           = moduser32.NewProc("EmptyClipboard")
	procGetClipboardData         = moduser32.NewProc("GetClipboardData")
	procSetClipboardData         = moduser32.NewProc("SetClipboardData")

	procGlobalAlloc  = modkernel32.NewProc("GlobalAlloc")
	procGlobalLock   = modkernel32.NewProc("GlobalLock")
	procGlobalUnlock = modkernel32.NewProc("GlobalUnlock")
	procGlobalFree   = modkernel32.NewProc("GlobalFree")
	procLstrlenW     = modkernel32.NewProc("lstrlenW")

	procImmGetContext           = modimm32.NewProc("ImmGetContext")
	procImmReleaseContext       = modimm32.NewProc("ImmReleaseContext")
	procImmGetConversionStatus  = modimm32.NewProc("ImmGetConversionStatus")
)

const (
	whKeyboardLL = 13

	wmKeyDown    = 0x0100
	wmSysKeyDown = 0x0104
	wmQuit       = 0x0012

	llkhfInjected = 0x10

	vkBack   = 0x08
	vkTab    = 0x09
	vkReturn = 0x0D
	vkEscape = 0x1B
	vkPrior  = 0x21
	vkNext   = 0x22
	vkEnd    = 0x23
	vkHome   = 0x24
	vkLeft   = 0x25
	vkUp     = 0x26
	vkRight  = 0x27
	vkDown   = 0x28
	vkLWin   = 0x5B
	vkRWin   = 0x5C
	vkLMenu  = 0xA4
	vkRMenu  = 0xA5
	vkLCtrl  = 0xA2
	vkRCtrl  = 0xA3
	vkLShift = 0xA0
	vkRShift = 0xA1

	keyeventfKeyup    = 0x0002
	keyeventfUnicode  = 0x0004
	keyeventfScancode = 0x0008

	inputKeyboard = 1

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	imeCmodeNative = 0x0001
)

type kbdLLHookStruct struct {
	VkCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keybdInput struct {
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
}

type input struct {
	Type uint32
	_    [4]byte
	Ki   keybdInput
	_    [8]byte
}

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
}

func SetExpansionHandler(handler func(storage.Snippet)) {
	keyboardEngine.mu.Lock()
	keyboardEngine.onExpansion = handler
	keyboardEngine.mu.Unlock()
}

func Start(store Store) bool {
	keyboardEngine.mu.Lock()
	if keyboardEngine.running {
		keyboardEngine.store = store
		keyboardEngine.mu.Unlock()
		return true
	}
	keyboardEngine.store = store
	keyboardEngine.mu.Unlock()

	readyCh := make(chan bool, 1)
	doneCh := make(chan struct{})

	keyboardEngine.mu.Lock()
	keyboardEngine.doneCh = doneCh
	keyboardEngine.mu.Unlock()

	go runHookLoop(readyCh, doneCh)
	ok := <-readyCh
	keyboardEngine.mu.Lock()
	keyboardEngine.running = ok
	if !ok {
		keyboardEngine.doneCh = nil
	}
	keyboardEngine.mu.Unlock()
	return ok
}

func Stop() {
	keyboardEngine.mu.Lock()
	tid := keyboardEngine.threadID
	doneCh := keyboardEngine.doneCh
	keyboardEngine.mu.Unlock()

	if tid != 0 {
		procPostThreadMessageW.Call(uintptr(tid), uintptr(wmQuit), 0, 0)
	}
	if doneCh != nil {
		<-doneCh
	}
	flushTypingCount()
	keyboardEngine.mu.Lock()
	keyboardEngine.running = false
	keyboardEngine.threadID = 0
	keyboardEngine.doneCh = nil
	keyboardEngine.mu.Unlock()
}

func Running() bool {
	keyboardEngine.mu.Lock()
	defer keyboardEngine.mu.Unlock()
	return keyboardEngine.running
}

func runHookLoop(readyCh chan<- bool, doneCh chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(doneCh)

	tid, _, _ := procGetCurrentThreadId.Call()
	keyboardEngine.mu.Lock()
	keyboardEngine.threadID = uint32(tid)
	keyboardEngine.mu.Unlock()

	hMod, _, _ := procGetModuleHandleW.Call(0)
	hHook, _, _ := procSetWindowsHookExW.Call(uintptr(whKeyboardLL), lowLevelKeyboardCallback, hMod, 0)
	if hHook == 0 {
		readyCh <- false
		return
	}
	defer procUnhookWindowsHookEx.Call(hHook)

	readyCh <- true

	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func lowLevelKeyboardProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode < 0 {
		ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
		return ret
	}
	if wParam != uintptr(wmKeyDown) && wParam != uintptr(wmSysKeyDown) {
		ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
		return ret
	}
	hook := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
	if hook.Flags&llkhfInjected != 0 {
		ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
		return ret
	}

	keyboardEngine.mu.Lock()
	suppressed := keyboardEngine.suppressInput
	keyboardEngine.mu.Unlock()
	if suppressed {
		ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
		return ret
	}

	vk := hook.VkCode
	switch vk {
	case vkBack:
		keyboardInput("", true)
	case vkReturn:
		keyboardInput("\n", false)
	case vkTab:
		keyboardInput("\t", false)
	default:
		if isPureModifierVK(vk) {
			break
		}
		text := translateVKToUnicode(vk, hook.ScanCode)
		if text != "" {
			keyboardInput(text, false)
		}
	}

	ret, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return ret
}

func isPureModifierVK(vk uint32) bool {
	switch vk {
	case vkLWin, vkRWin, vkLMenu, vkRMenu, vkLCtrl, vkRCtrl, vkLShift, vkRShift,
		0x10 /*VK_SHIFT*/, 0x11 /*VK_CONTROL*/, 0x12 /*VK_MENU*/, 0x14 /*VK_CAPITAL*/, 0x91 /*VK_SCROLL*/, 0x90 /*VK_NUMLOCK*/ :
		return true
	}
	return false
}

func translateVKToUnicode(vk, scan uint32) string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	var threadID uintptr
	if hwnd != 0 {
		var pid uint32
		tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		threadID = tid
	}
	hkl, _, _ := procGetKeyboardLayout.Call(threadID)

	var state [256]byte
	procGetKeyboardState.Call(uintptr(unsafe.Pointer(&state[0])))

	var buf [8]uint16
	const flagDontChangeKbdState = 0x4
	n, _, _ := procToUnicodeEx.Call(
		uintptr(vk),
		uintptr(scan),
		uintptr(unsafe.Pointer(&state[0])),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(flagDontChangeKbdState),
		hkl,
	)
	ni := int32(n)
	if ni <= 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:ni])
}

func keyboardInput(text string, backspace bool) {
	keyboardEngine.mu.Lock()
	store := keyboardEngine.store
	matcher := keyboardEngine.matcher
	keyboardEngine.mu.Unlock()
	if store == nil {
		return
	}

	if backspace {
		recordTyping(store, 1)
		keyboardEngine.mu.Lock()
		matcher.Backspace()
		keyboardEngine.mu.Unlock()
		return
	}
	if text == "" {
		return
	}
	recordTyping(store, 1)

	delimiterTyped := flow.IsDelimiter(text)
	keyboardEngine.mu.Lock()
	matcher.Push(text)
	buffer := matcher.Buffer()
	keyboardEngine.mu.Unlock()

	snippets, err := store.ListSnippets("")
	if err != nil {
		return
	}

	keyboardEngine.mu.Lock()
	match, ok := matcher.Find(snippets, delimiterTyped)
	if ok {
		matcher.Reset()
	}
	keyboardEngine.mu.Unlock()
	if !ok {
		return
	}

	keyboardEngine.mu.Lock()
	onExpansion := keyboardEngine.onExpansion
	keyboardEngine.mu.Unlock()
	if onExpansion != nil {
		onExpansion(match.Snippet)
	}

	deleteKeys := deleteKeysForMatch(buffer, text, match.Snippet.Shortcut, match.Snippet.CaseSensitive, delimiterTyped)
	deleteCount := shortcutDeleteCount(deleteKeys)
	if delimiterTyped && strings.HasSuffix(buffer, text) {
		deleteCount += len([]rune(text))
	}

	sendBackspaces(deleteCount)
	time.Sleep(backspaceSettleDuration(deleteCount))
	executeSnippetActions(renderSnippetActions(match.Snippet.Content), match.Snippet.UsePaste)
	_ = store.LogExpansion(match.Snippet.ID, "")
}

func recordTyping(store Store, count int64) {
	if store == nil || count <= 0 {
		return
	}

	var shouldFlush bool
	keyboardEngine.mu.Lock()
	keyboardEngine.typingCount += count
	if keyboardEngine.typingCount >= 25 {
		shouldFlush = true
	} else if !keyboardEngine.typingFlush {
		keyboardEngine.typingFlush = true
		time.AfterFunc(1200*time.Millisecond, flushTypingCount)
	}
	keyboardEngine.mu.Unlock()

	if shouldFlush {
		go flushTypingCount()
	}
}

func flushTypingCount() {
	keyboardEngine.mu.Lock()
	store := keyboardEngine.store
	count := keyboardEngine.typingCount
	keyboardEngine.typingCount = 0
	keyboardEngine.typingFlush = false
	keyboardEngine.mu.Unlock()

	if store == nil || count <= 0 {
		return
	}
	_ = store.LogTyping(count)
}

func deleteKeysForMatch(buffer string, delimiter string, shortcut string, caseSensitive bool, delimiterTyped bool) string {
	matchBuffer := buffer
	if delimiterTyped && strings.HasSuffix(matchBuffer, delimiter) {
		matchBuffer = trimLastRune(matchBuffer)
	}

	foldedBuffer := matchBuffer
	foldedShortcut := shortcut
	if !caseSensitive {
		foldedBuffer = strings.ToLower(foldedBuffer)
		foldedShortcut = strings.ToLower(foldedShortcut)
	}
	if !strings.HasSuffix(foldedBuffer, foldedShortcut) {
		return shortcut
	}

	bufferRunes := []rune(matchBuffer)
	shortcutRunes := []rune(shortcut)
	if len(shortcutRunes) == 0 || len(bufferRunes) < len(shortcutRunes) {
		return shortcut
	}

	start := len(bufferRunes) - len(shortcutRunes)
	deleteRunes := bufferRunes[start:]
	if start > 0 && isTriggerPrefix(bufferRunes[start-1]) && !isTriggerPrefix(shortcutRunes[0]) {
		deleteRunes = append([]rune{bufferRunes[start-1]}, deleteRunes...)
	}
	return string(deleteRunes)
}

func shortcutDeleteCount(deleteKeys string) int {
	return shortcutDeleteCountForInputMode(deleteKeys, koreanInputActive())
}

func shortcutDeleteCountForInputMode(deleteKeys string, koreanInputActive bool) int {
	typedLength := len([]rune(deleteKeys))
	if koreanInputActive {
		koreanLength := flow.KoreanTwoSetDisplayLength(deleteKeys)
		chordedLength := flow.KoreanTwoSetChordedDisplayLength(deleteKeys)
		if chordedLength > 0 && chordedLength < koreanLength {
			koreanLength = chordedLength
		}
		if koreanLength > 0 && koreanLength < typedLength {
			return koreanLength
		}
	}
	return typedLength
}

func backspaceSettleDuration(count int) time.Duration {
	if count <= 0 {
		return 20 * time.Millisecond
	}
	return time.Duration(count)*12*time.Millisecond + 48*time.Millisecond
}

func isTriggerPrefix(value rune) bool {
	switch value {
	case '`', ';', '/', '\\':
		return true
	default:
		return false
	}
}

func trimLastRune(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	return string(runes[:len(runes)-1])
}

type snippetAction struct {
	text    string
	keyCode int
}

var snippetTagPattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

func executeSnippetActions(actions []snippetAction, usePaste bool) {
	keyboardEngine.mu.Lock()
	keyboardEngine.suppressInput = true
	keyboardEngine.mu.Unlock()
	defer func() {
		keyboardEngine.mu.Lock()
		keyboardEngine.suppressInput = false
		keyboardEngine.mu.Unlock()
	}()

	for _, action := range actions {
		if action.text != "" {
			if usePaste {
				pasteText(action.text)
				time.Sleep(24 * time.Millisecond)
			} else {
				typeText(action.text)
				time.Sleep(16 * time.Millisecond)
			}
			continue
		}
		if action.keyCode > 0 {
			sendKey(uint16(action.keyCode))
			time.Sleep(12 * time.Millisecond)
		}
	}
}

func renderSnippetActions(content string) []snippetAction {
	matches := snippetTagPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return []snippetAction{{text: content}}
	}

	actions := []snippetAction{}
	cursor := 0
	for _, match := range matches {
		if match[0] > cursor {
			appendSnippetText(&actions, content[cursor:match[0]])
		}
		tag := strings.TrimSpace(content[match[2]:match[3]])
		if handled, actionText, keyCode := renderSnippetTag(tag); handled {
			if actionText != "" {
				appendSnippetText(&actions, actionText)
			}
			if keyCode > 0 {
				actions = append(actions, snippetAction{keyCode: keyCode})
			}
		} else {
			appendSnippetText(&actions, content[match[0]:match[1]])
		}
		cursor = match[1]
	}
	if cursor < len(content) {
		appendSnippetText(&actions, content[cursor:])
	}
	if len(actions) == 0 {
		return []snippetAction{{text: ""}}
	}
	return actions
}

func appendSnippetText(actions *[]snippetAction, value string) {
	if value == "" {
		return
	}
	last := len(*actions) - 1
	if last >= 0 && (*actions)[last].keyCode == 0 {
		(*actions)[last].text += value
		return
	}
	*actions = append(*actions, snippetAction{text: value})
}

func renderSnippetTag(tag string) (bool, string, int) {
	normalized := strings.ToLower(strings.TrimSpace(tag))
	if strings.HasPrefix(normalized, "date:") {
		return true, time.Now().Format(tag[len("date:"):]), 0
	}
	if strings.HasPrefix(normalized, "time:") {
		return true, time.Now().Format(tag[len("time:"):]), 0
	}
	switch normalized {
	case "clipboard", "paste":
		return true, clipboardText(), 0
	case "space", "spacebar":
		return true, " ", 0
	}
	if keyCode, ok := snippetKeyCode(normalized); ok {
		return true, "", keyCode
	}
	return false, "", 0
}

func snippetKeyCode(tag string) (int, bool) {
	switch strings.ReplaceAll(tag, " ", "") {
	case "tab":
		return vkTab, true
	case "return", "enter":
		return vkReturn, true
	case "esc", "escape":
		return vkEscape, true
	case "home":
		return vkHome, true
	case "end":
		return vkEnd, true
	case "pageup", "pgup":
		return vkPrior, true
	case "pagedown", "pgdn":
		return vkNext, true
	case "up", "arrowup":
		return vkUp, true
	case "down", "arrowdown":
		return vkDown, true
	case "left", "arrowleft":
		return vkLeft, true
	case "right", "arrowright":
		return vkRight, true
	default:
		return 0, false
	}
}

func koreanInputActive() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	himc, _, _ := procImmGetContext.Call(hwnd)
	if himc == 0 {
		return false
	}
	defer procImmReleaseContext.Call(hwnd, himc)
	var conv, sent uint32
	procImmGetConversionStatus.Call(himc, uintptr(unsafe.Pointer(&conv)), uintptr(unsafe.Pointer(&sent)))
	return (conv & imeCmodeNative) != 0
}

func sendBackspaces(count int) {
	if count <= 0 {
		return
	}
	inputs := make([]input, 0, count*2)
	for i := 0; i < count; i++ {
		inputs = append(inputs,
			input{Type: inputKeyboard, Ki: keybdInput{Vk: vkBack}},
			input{Type: inputKeyboard, Ki: keybdInput{Vk: vkBack, Flags: keyeventfKeyup}},
		)
	}
	sendInputBatch(inputs)
}

func sendKey(vk uint16) {
	inputs := []input{
		{Type: inputKeyboard, Ki: keybdInput{Vk: vk}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vk, Flags: keyeventfKeyup}},
	}
	sendInputBatch(inputs)
}

func typeText(text string) {
	if text == "" {
		return
	}
	utf16 := syscall.StringToUTF16(text)
	if len(utf16) > 0 && utf16[len(utf16)-1] == 0 {
		utf16 = utf16[:len(utf16)-1]
	}
	inputs := make([]input, 0, len(utf16)*2)
	for _, ch := range utf16 {
		inputs = append(inputs,
			input{Type: inputKeyboard, Ki: keybdInput{Scan: ch, Flags: keyeventfUnicode}},
			input{Type: inputKeyboard, Ki: keybdInput{Scan: ch, Flags: keyeventfUnicode | keyeventfKeyup}},
		)
	}
	sendInputBatch(inputs)
}

func pasteText(text string) {
	if text == "" {
		return
	}
	backup, hadBackup := readClipboardText()
	if !setClipboardText(text) {
		typeText(text)
		return
	}
	sendCtrlV()
	time.Sleep(24 * time.Millisecond)
	if hadBackup {
		setClipboardText(backup)
	}
}

func sendCtrlV() {
	const vkControl = 0x11
	const vkV = 0x56
	inputs := []input{
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkControl}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkV}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkV, Flags: keyeventfKeyup}},
		{Type: inputKeyboard, Ki: keybdInput{Vk: vkControl, Flags: keyeventfKeyup}},
	}
	sendInputBatch(inputs)
}

func sendInputBatch(inputs []input) {
	if len(inputs) == 0 {
		return
	}
	procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(input{}))
}

func clipboardText() string {
	text, _ := readClipboardText()
	return text
}

func readClipboardText() (string, bool) {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return "", false
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(uintptr(cfUnicodeText))
	if h == 0 {
		return "", false
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", false
	}
	defer procGlobalUnlock.Call(h)

	length, _, _ := procLstrlenW.Call(ptr)
	if length == 0 {
		return "", true
	}
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), int(length))
	return syscall.UTF16ToString(buf), true
}

func setClipboardText(text string) bool {
	utf16 := syscall.StringToUTF16(text)
	if len(utf16) == 0 {
		utf16 = []uint16{0}
	}
	size := uintptr(len(utf16) * 2)
	hMem, _, _ := procGlobalAlloc.Call(uintptr(gmemMoveable), size)
	if hMem == 0 {
		return false
	}
	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		procGlobalFree.Call(hMem)
		return false
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(utf16))
	copy(dst, utf16)
	procGlobalUnlock.Call(hMem)

	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		procGlobalFree.Call(hMem)
		return false
	}
	procEmptyClipboard.Call()
	set, _, _ := procSetClipboardData.Call(uintptr(cfUnicodeText), hMem)
	procCloseClipboard.Call()
	if set == 0 {
		procGlobalFree.Call(hMem)
		return false
	}
	return true
}
