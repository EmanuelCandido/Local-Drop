//go:build windows

package main

import (
	"errors"
	"math"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mainWindowClass  = "LocalDropMain"
	popupWindowClass = "LocalDropPopup"
	msgSynchronize   = wmApp + 1
	msgTray          = wmApp + 2
	timerPopup       = 1
)

var (
	windowHandlers   = map[uintptr]*canvasWindow{}
	creatingWindow   *canvasWindow
	classesOnce      sync.Once
	classesErr       error
	moduleHandle     uintptr
	wndProcCallback  uintptr
	msgTaskbarCreate uint32
	msgShowRequest   uint32
)

type nativeHit struct {
	id string
	r  rect
}

// canvasWindow é uma janela inteiramente desenhada pelo LocalDrop: recebe a
// pintura, os cliques e o teclado e repassa para callbacks.
type canvasWindow struct {
	hwnd   uintptr
	dpi    int
	buffer backBuffer
	cache  *gdiCache

	paint    func(p *painter, bounds rect)
	activate func(id string)
	key      func(vk int, ctrl, shift bool) bool
	wheel    func(delta int)
	message  func(message uint32, wParam, lParam uintptr) (uintptr, bool)
	close    func() bool // devolve true para permitir fechar
	dropped  func(paths []string)

	hits          []nativeHit
	hover         string
	pressed       string
	focus         string
	focusVisible  bool
	tracking      bool
	minW, minH    int
	closed        bool
	background    color
	focusRingSkip bool
}

func (w *canvasWindow) scale() float64 { return float64(w.dpi) / 96 }

func (w *canvasWindow) add(id string, r rect) { w.hits = append(w.hits, nativeHit{id, r}) }

func (w *canvasWindow) hit(x, y int) string {
	for index := len(w.hits) - 1; index >= 0; index-- {
		r := w.hits[index].r
		if x >= r.X && y >= r.Y && x < r.X+r.Width && y < r.Y+r.Height {
			return w.hits[index].id
		}
	}
	return ""
}

func (w *canvasWindow) invalidate() {
	if w.hwnd != 0 {
		call(procInvalidateRect, w.hwnd, 0, 0)
	}
}

func (w *canvasWindow) logicalPoint(lParam uintptr) (int, int) {
	scale := w.scale()
	return int(float64(loword(lParam)) / scale), int(float64(hiword(lParam)) / scale)
}

func (w *canvasWindow) clientBounds() rect {
	var client rect32
	call(procGetClientRect, w.hwnd, uintptr(unsafe.Pointer(&client)))
	scale := w.scale()
	return rect{Width: int(math.Round(float64(client.Right) / scale)), Height: int(math.Round(float64(client.Bottom) / scale))}
}

// focusOrder lista os alvos clicáveis na ordem em que foram desenhados.
func (w *canvasWindow) focusOrder() []string {
	seen := make(map[string]bool, len(w.hits))
	order := make([]string, 0, len(w.hits))
	for _, hit := range w.hits {
		if !seen[hit.id] {
			seen[hit.id] = true
			order = append(order, hit.id)
		}
	}
	return order
}

func (w *canvasWindow) moveFocus(backwards bool) {
	order := w.focusOrder()
	if len(order) == 0 {
		return
	}
	index := -1
	for position, id := range order {
		if id == w.focus {
			index = position
			break
		}
	}
	switch {
	case index < 0 && backwards:
		index = len(order) - 1
	case index < 0:
		index = 0
	case backwards:
		index = (index - 1 + len(order)) % len(order)
	default:
		index = (index + 1) % len(order)
	}
	w.focus = order[index]
	w.focusVisible = true
	w.invalidate()
}

func (w *canvasWindow) focusRect() (rect, bool) {
	for _, hit := range w.hits {
		if hit.id == w.focus {
			return hit.r, true
		}
	}
	return rect{}, false
}

func (w *canvasWindow) doPaint() {
	var ps paintStruct
	dc := call(procBeginPaint, w.hwnd, uintptr(unsafe.Pointer(&ps)))
	var client rect32
	call(procGetClientRect, w.hwnd, uintptr(unsafe.Pointer(&client)))
	if client.Right > 0 && client.Bottom > 0 {
		w.buffer.ensure(dc, client.Right, client.Bottom)
		p := newPainter(w.buffer.dc, w.dpi, w.cache)
		bounds := w.clientBounds()
		w.hits = w.hits[:0]
		p.fill(w.background, bounds)
		if w.paint != nil {
			w.paint(p, bounds)
		}
		if w.focusVisible && !w.focusRingSkip {
			if r, ok := w.focusRect(); ok {
				p.outlineWidth(primary, rect{X: r.X - 3, Y: r.Y - 3, Width: r.Width + 6, Height: r.Height + 6}, 13, 2, false)
			} else {
				w.focus = ""
			}
		}
		call(procBitBlt, dc, 0, 0, uintptr(client.Right), uintptr(client.Bottom), w.buffer.dc, 0, 0, srcCopy)
	}
	call(procEndPaint, w.hwnd, uintptr(unsafe.Pointer(&ps)))
}

func (w *canvasWindow) handle(message uint32, wParam, lParam uintptr) (uintptr, bool) {
	if w.message != nil {
		if result, handled := w.message(message, wParam, lParam); handled {
			return result, true
		}
	}
	switch message {
	case wmEraseBkgnd:
		return 1, true
	case wmPaint:
		w.doPaint()
		return 0, true
	case wmSize:
		w.invalidate()
		return 0, true
	case wmGetMinMaxInfo:
		if w.minW > 0 {
			info := (*minMaxInfo)(pointerFrom(lParam))
			info.MinTrackSize = point32{X: int32(float64(w.minW) * w.scale()), Y: int32(float64(w.minH) * w.scale())}
		}
		return 0, true
	case wmDpiChanged:
		w.dpi = hiword(wParam)
		if w.dpi <= 0 {
			w.dpi = loword(wParam)
		}
		w.cache.dropFonts()
		suggested := (*rect32)(pointerFrom(lParam))
		call(procSetWindowPos, w.hwnd, 0, uintptr(suggested.Left), uintptr(suggested.Top),
			uintptr(suggested.Right-suggested.Left), uintptr(suggested.Bottom-suggested.Top), swpNoZOrder|swpNoActivate)
		w.invalidate()
		return 0, true
	case wmSetCursor:
		if loword(lParam) == 1 /* HTCLIENT */ && w.hover != "" {
			call(procSetCursor, call(procLoadCursorW, 0, idcHand))
			return 1, true
		}
	case wmMouseMove:
		if !w.tracking {
			track := trackMouseEventInfo{Flags: tmeLeave, Hwnd: w.hwnd}
			track.Size = uint32(unsafe.Sizeof(track))
			call(procTrackMouseEvent, uintptr(unsafe.Pointer(&track)))
			w.tracking = true
		}
		x, y := w.logicalPoint(lParam)
		if current := w.hit(x, y); current != w.hover {
			w.hover = current
			w.invalidate()
		}
		return 0, true
	case wmMouseLeave:
		w.tracking = false
		if w.hover != "" {
			w.hover = ""
			w.invalidate()
		}
		return 0, true
	case wmLButtonDown:
		x, y := w.logicalPoint(lParam)
		w.pressed = w.hit(x, y)
		w.focusVisible = false
		call(procSetFocus, w.hwnd)
		call(procSetCapture, w.hwnd)
		w.invalidate()
		return 0, true
	case wmLButtonUp:
		call(procReleaseCapture)
		x, y := w.logicalPoint(lParam)
		id := w.hit(x, y)
		same := id != "" && id == w.pressed
		w.pressed = ""
		if same {
			w.focus = id
			if w.activate != nil {
				w.activate(id)
			}
		}
		w.invalidate()
		return 0, true
	case wmMouseWheel:
		if w.wheel != nil {
			w.wheel(hiword(wParam))
		}
		return 0, true
	case wmKeyDown:
		ctrl, shift := keyDown(vkControl), keyDown(vkShift)
		vk := int(wParam)
		if w.key != nil && w.key(vk, ctrl, shift) {
			return 0, true
		}
		switch vk {
		case vkTab:
			w.moveFocus(shift)
			return 0, true
		case vkReturn, vkSpace:
			if w.focus != "" && w.activate != nil {
				if _, ok := w.focusRect(); ok {
					w.activate(w.focus)
					w.invalidate()
				}
			}
			return 0, true
		}
	case wmGetDlgCode:
		return dlgcWantAllKeys, true
	case wmDropFiles:
		if w.dropped != nil {
			w.dropped(droppedPaths(wParam))
		}
		call(procDragFinish, wParam)
		return 0, true
	case wmClose:
		if w.close == nil || w.close() {
			w.closed = true
			call(procDestroyWindow, w.hwnd)
		}
		return 0, true
	case wmDestroy:
		w.buffer.release()
		delete(windowHandlers, w.hwnd)
		return 0, true
	}
	return 0, false
}

func droppedPaths(drop uintptr) []string {
	count := int(call(procDragQueryFileW, drop, 0xFFFFFFFF, 0, 0))
	paths := make([]string, 0, count)
	for index := 0; index < count; index++ {
		size := call(procDragQueryFileW, drop, uintptr(index), 0, 0) + 1
		buffer := make([]uint16, size)
		call(procDragQueryFileW, drop, uintptr(index), uintptr(unsafe.Pointer(&buffer[0])), size)
		paths = append(paths, windows.UTF16ToString(buffer))
	}
	return paths
}

func windowProcedure(hwnd, message, wParam, lParam uintptr) uintptr {
	w := windowHandlers[hwnd]
	if w == nil && creatingWindow != nil {
		w = creatingWindow
		w.hwnd = hwnd
		windowHandlers[hwnd] = w
	}
	if w != nil {
		if result, handled := w.handle(uint32(message), wParam, lParam); handled {
			return result
		}
	}
	return call(procDefWindowProcW, hwnd, message, wParam, lParam)
}

func registerClasses(icon, smallIcon uintptr) error {
	classesOnce.Do(func() {
		moduleHandle = call(procGetModuleHandleW, 0)
		wndProcCallback = syscall.NewCallback(windowProcedure)
		msgTaskbarCreate = uint32(call(procRegisterWindowMessageW, uintptr(unsafe.Pointer(utf16Ptr("TaskbarCreated")))))
		msgShowRequest = uint32(call(procRegisterWindowMessageW, uintptr(unsafe.Pointer(utf16Ptr("LocalDrop.ShowWindow")))))
		for _, name := range []string{mainWindowClass, popupWindowClass} {
			class := wndClassEx{
				Style: 0x0008 /* CS_DBLCLKS */, WndProc: wndProcCallback, Instance: moduleHandle,
				Icon: icon, IconSm: smallIcon, Cursor: call(procLoadCursorW, 0, idcArrow), ClassName: utf16Ptr(name),
			}
			class.Size = uint32(unsafe.Sizeof(class))
			if call(procRegisterClassExW, uintptr(unsafe.Pointer(&class))) == 0 {
				classesErr = errors.New("não foi possível registrar a janela " + name)
				return
			}
		}
	})
	return classesErr
}

func systemDPI() int {
	if procGetDpiForSystem.Find() == nil {
		if dpi := int(call(procGetDpiForSystem)); dpi > 0 {
			return dpi
		}
	}
	dc := call(procGetDC, 0)
	defer call(procReleaseDC, 0, dc)
	if dpi := int(call(procGetDeviceCaps, dc, logPixelsX)); dpi > 0 {
		return dpi
	}
	return 96
}

func windowDPI(hwnd uintptr) int {
	if procGetDpiForWindow.Find() == nil {
		if dpi := int(call(procGetDpiForWindow, hwnd)); dpi > 0 {
			return dpi
		}
	}
	return systemDPI()
}

// outerSize converte um tamanho de área útil (lógico) em tamanho da janela.
func outerSize(width, height int, style, exStyle uint32, dpi int) (int32, int32) {
	scale := float64(dpi) / 96
	area := rect32{Right: int32(float64(width) * scale), Bottom: int32(float64(height) * scale)}
	if procAdjustWindowRectExForDpi.Find() == nil {
		call(procAdjustWindowRectExForDpi, uintptr(unsafe.Pointer(&area)), uintptr(style), 0, uintptr(exStyle), uintptr(dpi))
	}
	return area.Right - area.Left, area.Bottom - area.Top
}

// centerOn devolve a posição para centralizar um retângulo sobre outro, sem
// sair da área de trabalho do monitor.
func centerOn(owner uintptr, width, height int32) (int32, int32) {
	var work rect32
	var base rect32
	if owner != 0 && call(procIsWindowVisible, owner) != 0 && call(procIsIconic, owner) == 0 {
		call(procGetWindowRect, owner, uintptr(unsafe.Pointer(&base)))
		monitor := call(procMonitorFromWindow, owner, monitorDefaultToNearest)
		info := monitorInfo{}
		info.Size = uint32(unsafe.Sizeof(info))
		call(procGetMonitorInfoW, monitor, uintptr(unsafe.Pointer(&info)))
		work = info.Work
	} else {
		call(procSystemParametersInfoW, spiGetWorkArea, 0, uintptr(unsafe.Pointer(&work)), 0)
		base = work
	}
	x := base.Left + (base.Right-base.Left-width)/2
	y := base.Top + (base.Bottom-base.Top-height)/2
	if x+width > work.Right {
		x = work.Right - width
	}
	if y+height > work.Bottom {
		y = work.Bottom - height
	}
	if x < work.Left {
		x = work.Left
	}
	if y < work.Top {
		y = work.Top
	}
	return x, y
}

func createCanvasWindow(w *canvasWindow, class, title string, style, exStyle uint32, owner uintptr, width, height int) error {
	dpi := systemDPI()
	if owner != 0 {
		dpi = windowDPI(owner)
	}
	w.dpi = dpi
	outerW, outerH := outerSize(width, height, style, exStyle, dpi)
	x, y := centerOn(owner, outerW, outerH)
	creatingWindow = w
	hwnd := call(procCreateWindowExW, uintptr(exStyle), uintptr(unsafe.Pointer(utf16Ptr(class))), uintptr(unsafe.Pointer(utf16Ptr(title))),
		uintptr(style), uintptr(x), uintptr(y), uintptr(outerW), uintptr(outerH), owner, 0, moduleHandle, 0)
	creatingWindow = nil
	if hwnd == 0 {
		return errors.New("não foi possível criar a janela")
	}
	w.hwnd = hwnd
	windowHandlers[hwnd] = w
	if real := windowDPI(hwnd); real != dpi {
		w.dpi = real
		outerW, outerH = outerSize(width, height, style, exStyle, real)
		x, y = centerOn(owner, outerW, outerH)
		call(procSetWindowPos, hwnd, 0, uintptr(x), uintptr(y), uintptr(outerW), uintptr(outerH), swpNoZOrder|swpNoActivate)
	}
	return nil
}

// runModal mostra uma janela filha bloqueando a principal até ser fechada.
func runModal(owner uintptr, w *canvasWindow, filter func(m *msg) bool) {
	call(procEnableWindow, owner, 0)
	call(procShowWindow, w.hwnd, swShowNormal)
	call(procSetForegroundWindow, w.hwnd)
	var m msg
	for !w.closed {
		result := int32(call(procGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0))
		if result == 0 {
			call(procPostQuitMessage, m.WParam)
			break
		}
		if result < 0 {
			break
		}
		if filter != nil && filter(&m) {
			continue
		}
		call(procTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(procDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
	call(procEnableWindow, owner, 1)
	if !w.closed {
		w.closed = true
		call(procDestroyWindow, w.hwnd)
	}
	call(procSetForegroundWindow, owner)
}

func runMessageLoop() {
	var m msg
	for {
		result := int32(call(procGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0))
		if result <= 0 {
			return
		}
		call(procTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(procDispatchMessageW, uintptr(unsafe.Pointer(&m)))
	}
}

// ---------------------------------------------------------------- sincronização

// uiQueue leva trabalho das goroutines do servidor para a thread da janela.
type uiQueue struct {
	mu      sync.Mutex
	pending []func()
	hwnd    uintptr
}

func (q *uiQueue) post(action func()) {
	q.mu.Lock()
	q.pending = append(q.pending, action)
	hwnd := q.hwnd
	q.mu.Unlock()
	if hwnd != 0 {
		call(procPostMessageW, hwnd, msgSynchronize, 0, 0)
	}
}

func (q *uiQueue) drain() {
	q.mu.Lock()
	actions := q.pending
	q.pending = nil
	q.mu.Unlock()
	for _, action := range actions {
		action()
	}
}

// ---------------------------------------------------------------- campo de texto nativo

type editField struct {
	hwnd      uintptr
	area      rect
	multiline bool
}

func newEditField(parent *canvasWindow, area rect, text string, multiline bool, limit int, f font) *editField {
	style := uint32(wsChild | wsVisible | wsTabStop)
	if multiline {
		style |= esMultiline | esAutoVScroll | esWantReturn | wsVScroll
	} else {
		style |= esAutoHScroll
	}
	scale := parent.scale()
	hwnd := call(procCreateWindowExW, 0, uintptr(unsafe.Pointer(utf16Ptr("EDIT"))), uintptr(unsafe.Pointer(utf16Ptr(text))), uintptr(style),
		uintptr(int32(float64(area.X)*scale)), uintptr(int32(float64(area.Y)*scale)),
		uintptr(int32(float64(area.Width)*scale)), uintptr(int32(float64(area.Height)*scale)),
		parent.hwnd, 0, moduleHandle, 0)
	if hwnd == 0 {
		return nil
	}
	call(procSendMessageW, hwnd, wmSetFont, parent.cache.font(f.points, f.bold, false, parent.dpi), 1)
	if limit > 0 {
		call(procSendMessageW, hwnd, emLimitText, uintptr(limit), 0)
	}
	margin := uintptr(int32(6 * scale))
	call(procSendMessageW, hwnd, emSetMargins, ecLeftMargin|ecRightMargin, margin|margin<<16)
	return &editField{hwnd: hwnd, area: area, multiline: multiline}
}

func (e *editField) text() string {
	length := int(call(procGetWindowTextLengthW, e.hwnd))
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	call(procGetWindowTextW, e.hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(length+1))
	return windows.UTF16ToString(buffer)
}

func (e *editField) focus(selectAll bool) {
	call(procSetFocus, e.hwnd)
	if selectAll {
		call(procSendMessageW, e.hwnd, emSetSel, 0, ^uintptr(0))
	}
}

// ---------------------------------------------------------------- área de transferência

func clipboardText(owner uintptr) (string, error) {
	if call(procOpenClipboard, owner) == 0 {
		return "", errors.New("a área de transferência está em uso")
	}
	defer call(procCloseClipboard)
	handle := call(procGetClipboardData, cfUnicodeText)
	if handle == 0 {
		return "", nil
	}
	pointer := call(procGlobalLock, handle)
	if pointer == 0 {
		return "", errors.New("não foi possível ler a área de transferência")
	}
	defer call(procGlobalUnlock, handle)
	return windows.UTF16PtrToString((*uint16)(pointerFrom(pointer))), nil
}

func setClipboardText(owner uintptr, text string) error {
	encoded, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	if call(procOpenClipboard, owner) == 0 {
		return errors.New("a área de transferência está em uso")
	}
	defer call(procCloseClipboard)
	call(procEmptyClipboard)
	size := uintptr(len(encoded) * 2)
	handle := call(procGlobalAlloc, gmemMoveable, size)
	if handle == 0 {
		return errors.New("memória insuficiente")
	}
	pointer := call(procGlobalLock, handle)
	copy(unsafe.Slice((*uint16)(pointerFrom(pointer)), len(encoded)), encoded)
	call(procGlobalUnlock, handle)
	if call(procSetClipboardData, cfUnicodeText, handle) == 0 {
		call(procGlobalFree, handle)
		return errors.New("não foi possível copiar")
	}
	return nil
}

// ---------------------------------------------------------------- diálogos do Windows (IFileOpenDialog)

var (
	clsidFileOpenDialog = windows.GUID{Data1: 0xDC1C5A9C, Data2: 0xE88A, Data3: 0x4DDE, Data4: [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidFileOpenDialog   = windows.GUID{Data1: 0xD57C7288, Data2: 0xD4AD, Data3: 0x4768, Data4: [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
	iidShellItem        = windows.GUID{Data1: 0x43826D1E, Data2: 0xE718, Data3: 0x42EE, Data4: [8]byte{0xBC, 0x55, 0xA1, 0xE2, 0x61, 0xC3, 0x7B, 0xFE}}
)

const (
	fosPickFolders      = 0x00000020
	fosForceFileSystem  = 0x00000040
	fosAllowMultiSelect = 0x00000200
	fosPathMustExist    = 0x00000800
	fosFileMustExist    = 0x00001000
	sigdnFileSysPath    = 0x80058000
	hresultCancelled    = 0x800704C7
)

func shellItemPath(item uintptr) string {
	var pointer uintptr
	if comCall(item, 5, sigdnFileSysPath, uintptr(unsafe.Pointer(&pointer))) != 0 || pointer == 0 {
		return ""
	}
	defer call(procCoTaskMemFree, pointer)
	return windows.UTF16PtrToString((*uint16)(pointerFrom(pointer)))
}

// showOpenDialog abre o seletor moderno do Windows. Devolve ok=false quando a
// pessoa cancela.
func showOpenDialog(owner uintptr, title, initial string, folders, multiple bool) ([]string, bool, error) {
	var dialog uintptr
	if hr := call(procCoCreateInstance, uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, 1 /* CLSCTX_INPROC_SERVER */, uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dialog))); hr != 0 || dialog == 0 {
		return nil, false, errors.New("o seletor de arquivos do Windows não abriu")
	}
	defer comCall(dialog, 2)
	var options uintptr
	comCall(dialog, 10, uintptr(unsafe.Pointer(&options)))
	options |= fosForceFileSystem | fosPathMustExist
	if folders {
		options |= fosPickFolders
	} else {
		options |= fosFileMustExist
	}
	if multiple {
		options |= fosAllowMultiSelect
	}
	comCall(dialog, 9, options)
	comCall(dialog, 17, uintptr(unsafe.Pointer(utf16Ptr(title))))
	if initial != "" {
		var folder uintptr
		if call(procSHCreateItem, uintptr(unsafe.Pointer(utf16Ptr(initial))), 0, uintptr(unsafe.Pointer(&iidShellItem)), uintptr(unsafe.Pointer(&folder))) == 0 && folder != 0 {
			comCall(dialog, 12, folder)
			comCall(folder, 2)
		}
	}
	hr := comCall(dialog, 3, owner)
	if uint32(hr) == hresultCancelled {
		return nil, false, nil
	}
	if hr != 0 {
		return nil, false, errors.New("o seletor de arquivos do Windows falhou")
	}
	paths := make([]string, 0, 1)
	if multiple {
		var items uintptr
		if comCall(dialog, 27, uintptr(unsafe.Pointer(&items))) != 0 || items == 0 {
			return nil, false, errors.New("não foi possível ler a seleção")
		}
		defer comCall(items, 2)
		var count uint32
		comCall(items, 7, uintptr(unsafe.Pointer(&count)))
		for index := uint32(0); index < count; index++ {
			var item uintptr
			if comCall(items, 8, uintptr(index), uintptr(unsafe.Pointer(&item))) == 0 && item != 0 {
				if path := shellItemPath(item); path != "" {
					paths = append(paths, path)
				}
				comCall(item, 2)
			}
		}
	} else {
		var item uintptr
		if comCall(dialog, 20, uintptr(unsafe.Pointer(&item))) != 0 || item == 0 {
			return nil, false, errors.New("não foi possível ler a seleção")
		}
		if path := shellItemPath(item); path != "" {
			paths = append(paths, path)
		}
		comCall(item, 2)
	}
	return paths, len(paths) > 0, nil
}

func pickDirectory(owner uintptr, initial string) (string, bool, error) {
	paths, ok, err := showOpenDialog(owner, "Escolha onde salvar os arquivos recebidos", initial, true, false)
	if err != nil || !ok {
		return "", false, err
	}
	return paths[0], true, nil
}

// ---------------------------------------------------------------- bandeja

type trayIcon struct {
	hwnd    uintptr
	icon    uintptr
	added   bool
	tooltip string
}

func (t *trayIcon) data(flags uint32) notifyIconData {
	data := notifyIconData{Hwnd: t.hwnd, ID: 1, Flags: flags, CallbackMessage: msgTray, Icon: t.icon}
	data.Size = uint32(unsafe.Sizeof(data))
	copyUTF16(data.Tip[:], t.tooltip)
	return data
}

func (t *trayIcon) add() bool {
	data := t.data(nifMessage | nifIcon | nifTip)
	t.added = call(procShellNotifyIconW, nimAdd, uintptr(unsafe.Pointer(&data))) != 0
	return t.added
}

func (t *trayIcon) setTooltip(text string) {
	if runes := []rune(text); len(runes) > 120 {
		text = string(runes[:119]) + "…"
	}
	if text == t.tooltip {
		return
	}
	t.tooltip = text
	if t.added {
		data := t.data(nifTip)
		call(procShellNotifyIconW, nimModify, uintptr(unsafe.Pointer(&data)))
	}
}

func (t *trayIcon) balloon(title, text string) {
	if !t.added {
		return
	}
	data := t.data(nifInfo)
	copyUTF16(data.InfoTitle[:], title)
	copyUTF16(data.Info[:], text)
	data.InfoFlags = niifInfo
	call(procShellNotifyIconW, nimModify, uintptr(unsafe.Pointer(&data)))
}

func (t *trayIcon) remove() {
	if t.added {
		data := t.data(0)
		call(procShellNotifyIconW, nimDelete, uintptr(unsafe.Pointer(&data)))
		t.added = false
	}
}

type menuItem struct {
	text string
	id   int
}

// popupMenu mostra um menu junto ao cursor e devolve o item escolhido (0 = nenhum).
func popupMenu(hwnd uintptr, items []menuItem) int {
	menu := call(procCreatePopupMenu)
	defer call(procDestroyMenu, menu)
	for _, item := range items {
		if item.id == 0 {
			call(procAppendMenuW, menu, mfSeparator, 0, 0)
			continue
		}
		call(procAppendMenuW, menu, mfString, uintptr(item.id), uintptr(unsafe.Pointer(utf16Ptr(item.text))))
	}
	var cursor point32
	call(procGetCursorPos, uintptr(unsafe.Pointer(&cursor)))
	call(procSetForegroundWindow, hwnd)
	choice := int(call(procTrackPopupMenu, menu, tpmReturn|tpmRightBtn|tpmBottom, uintptr(cursor.X), uintptr(cursor.Y), 0, hwnd, 0))
	call(procPostMessageW, hwnd, wmNull, 0, 0)
	return choice
}

// ---------------------------------------------------------------- utilidades

func loadAppIcon(size int) uintptr {
	return call(procLoadImageW, moduleHandleOrLoad(), uintptr(appIconResourceID), imageIcon, uintptr(size), uintptr(size), 0)
}

func moduleHandleOrLoad() uintptr {
	if moduleHandle == 0 {
		moduleHandle = call(procGetModuleHandleW, 0)
	}
	return moduleHandle
}

func shellOpen(target string) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("o arquivo não está mais disponível")
	}
	if result := call(procShellExecuteW, 0, uintptr(unsafe.Pointer(utf16Ptr("open"))), uintptr(unsafe.Pointer(utf16Ptr(target))), 0, 0, swShowNormal); result <= 32 {
		return errors.New("o Windows não conseguiu abrir")
	}
	return nil
}

func lockUIThread() {
	runtime.LockOSThread()
	call(procCoInitializeEx, 0, 0x2 /* COINIT_APARTMENTTHREADED */)
}
