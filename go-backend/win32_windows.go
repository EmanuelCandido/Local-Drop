//go:build windows

package main

// Chamadas Win32 usadas pela janela. Ficam aqui, sem bibliotecas de interface,
// para o executável carregar só o que a janela desenhada à mão precisa.

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	msimg32  = windows.NewLazySystemDLL("msimg32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procUpdateWindow             = user32.NewProc("UpdateWindow")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procInvalidateRect           = user32.NewProc("InvalidateRect")
	procBeginPaint               = user32.NewProc("BeginPaint")
	procEndPaint                 = user32.NewProc("EndPaint")
	procGetClientRect            = user32.NewProc("GetClientRect")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetDC                    = user32.NewProc("GetDC")
	procReleaseDC                = user32.NewProc("ReleaseDC")
	procFillRect                 = user32.NewProc("FillRect")
	procDrawTextW                = user32.NewProc("DrawTextW")
	procLoadCursorW              = user32.NewProc("LoadCursorW")
	procSetCursor                = user32.NewProc("SetCursor")
	procLoadImageW               = user32.NewProc("LoadImageW")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procSetFocus                 = user32.NewProc("SetFocus")
	procGetFocus                 = user32.NewProc("GetFocus")
	procEnableWindow             = user32.NewProc("EnableWindow")
	procGetKeyState              = user32.NewProc("GetKeyState")
	procTrackMouseEvent          = user32.NewProc("TrackMouseEvent")
	procSetCapture               = user32.NewProc("SetCapture")
	procReleaseCapture           = user32.NewProc("ReleaseCapture")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	procSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	procMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procGetClipboardData         = user32.NewProc("GetClipboardData")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procSetWindowTextW           = user32.NewProc("SetWindowTextW")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procSetTimer                 = user32.NewProc("SetTimer")
	procKillTimer                = user32.NewProc("KillTimer")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procScreenToClient           = user32.NewProc("ScreenToClient")
	procIsDialogMessageW         = user32.NewProc("IsDialogMessageW")
	procAdjustWindowRectExForDpi = user32.NewProc("AdjustWindowRectExForDpi")

	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	procCreatePen              = gdi32.NewProc("CreatePen")
	procExtCreatePen           = gdi32.NewProc("ExtCreatePen")
	procGetStockObject         = gdi32.NewProc("GetStockObject")
	procRoundRect              = gdi32.NewProc("RoundRect")
	procEllipse                = gdi32.NewProc("Ellipse")
	procSetTextColor           = gdi32.NewProc("SetTextColor")
	procSetBkMode              = gdi32.NewProc("SetBkMode")
	procSetBkColor             = gdi32.NewProc("SetBkColor")
	procCreateFontW            = gdi32.NewProc("CreateFontW")
	procGetDeviceCaps          = gdi32.NewProc("GetDeviceCaps")
	procGdiFlush               = gdi32.NewProc("GdiFlush")

	procAlphaBlend = msimg32.NewProc("AlphaBlend")

	procShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	procDragAcceptFiles  = shell32.NewProc("DragAcceptFiles")
	procDragQueryFileW   = shell32.NewProc("DragQueryFileW")
	procDragFinish       = shell32.NewProc("DragFinish")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
	procSHCreateItem     = shell32.NewProc("SHCreateItemFromParsingName")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")

	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")
)

const (
	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmSize           = 0x0005
	wmActivate       = 0x0006
	wmSetFocus       = 0x0007
	wmKillFocus      = 0x0008
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmEraseBkgnd     = 0x0014
	wmShowWindow     = 0x0018
	wmSetCursor      = 0x0020
	wmGetMinMaxInfo  = 0x0024
	wmSetFont        = 0x0030
	wmGetDlgCode     = 0x0087
	wmKeyDown        = 0x0100
	wmChar           = 0x0102
	wmSysKeyDown     = 0x0104
	wmCommand        = 0x0111
	wmTimer          = 0x0113
	wmCtlColorEdit   = 0x0133
	wmCtlColorStatic = 0x0138
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmRButtonUp      = 0x0205
	wmMouseWheel     = 0x020A
	wmDropFiles      = 0x0233
	wmMouseLeave     = 0x02A3
	wmDpiChanged     = 0x02E0
	wmContextMenu    = 0x007B
	wmNull           = 0x0000
	wmApp            = 0x8000
	wmUser           = 0x0400

	wsOverlappedWindow = 0x00CF0000
	wsPopup            = 0x80000000
	wsCaption          = 0x00C00000
	wsSysMenu          = 0x00080000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	wsVScroll          = 0x00200000
	wsExDlgModalFrame  = 0x00000001
	wsExAcceptFiles    = 0x00000010
	wsExToolWindow     = 0x00000080

	esMultiline   = 0x0004
	esAutoVScroll = 0x0040
	esAutoHScroll = 0x0080
	esWantReturn  = 0x1000
	emSetSel      = 0x00B1
	emLimitText   = 0x00C5
	emSetMargins  = 0x00D3
	ecLeftMargin  = 0x0001
	ecRightMargin = 0x0002

	swHide        = 0
	swShowNormal  = 1
	swShow        = 5
	swRestore     = 9
	swShowDefault = 10

	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002

	cwUseDefault = 0x80000000

	idcArrow = 32512
	idcHand  = 32649
	idcIBeam = 32513

	vkBack    = 0x08
	vkTab     = 0x09
	vkReturn  = 0x0D
	vkShift   = 0x10
	vkControl = 0x11
	vkEscape  = 0x1B
	vkSpace   = 0x20
	vkPrior   = 0x21
	vkNext    = 0x22
	vkEnd     = 0x23
	vkHome    = 0x24
	vkLeft    = 0x25
	vkUp      = 0x26
	vkRight   = 0x27
	vkDown    = 0x28

	dtCenter       = 0x00000001
	dtRight        = 0x00000002
	dtVCenter      = 0x00000004
	dtWordBreak    = 0x00000010
	dtSingleLine   = 0x00000020
	dtCalcRect     = 0x00000400
	dtNoPrefix     = 0x00000800
	dtEndEllipsis  = 0x00008000
	dtPathEllipsis = 0x00004000
	dtEditControl  = 0x00002000

	psSolid       = 0
	psDash        = 1
	psGeometric   = 0x00010000
	psEndcapFlat  = 0x00000200
	psJoinMiter   = 0x00002000
	nullBrush     = 5
	nullPen       = 8
	transparentBk = 1

	srcCopy     = 0x00CC0020
	logPixelsX  = 88
	biRGB       = 0
	dibRGBColor = 0
	acSrcOver   = 0x00
	acSrcAlpha  = 0x01

	imageIcon     = 1
	lrDefaultSize = 0x00000040
	lrShared      = 0x00008000

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	nifInfo       = 0x00000010
	niifInfo      = 0x00000001
	ninBalloonClk = wmUser + 5

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	tpmReturn   = 0x0100
	tpmRightBtn = 0x0002
	tpmBottom   = 0x0020

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	tmeLeave = 0x00000002

	dlgcWantAllKeys = 0x0004
	dlgcWantChars   = 0x0080
	dlgcHasSetSel   = 0x0008

	monitorDefaultToNearest = 0x00000002
	spiGetWorkArea          = 0x0030

	mbIconError = 0x00000010
)

type rect32 struct{ Left, Top, Right, Bottom int32 }

type point32 struct{ X, Y int32 }

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point32
	private uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type paintStruct struct {
	Hdc         uintptr
	Erase       int32
	Paint       rect32
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

type minMaxInfo struct {
	Reserved     point32
	MaxSize      point32
	MaxPosition  point32
	MinTrackSize point32
	MaxTrackSize point32
}

type trackMouseEventInfo struct {
	Size      uint32
	Flags     uint32
	Hwnd      uintptr
	HoverTime uint32
}

type monitorInfo struct {
	Size    uint32
	Monitor rect32
	Work    rect32
	Flags   uint32
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type logBrush struct {
	Style uint32
	Color uint32
	Hatch uintptr
}

type notifyIconData struct {
	Size            uint32
	Hwnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     uintptr
}

func utf16Ptr(text string) *uint16 {
	pointer, err := windows.UTF16PtrFromString(text)
	if err != nil {
		pointer, _ = windows.UTF16PtrFromString("")
	}
	return pointer
}

func loword(value uintptr) int { return int(int16(uint16(value))) }
func hiword(value uintptr) int { return int(int16(uint16(value >> 16))) }

// call chama uma função do Windows. A diretiva abaixo leva para o heap tudo o
// que foi convertido em uintptr nos argumentos: sem ela, o coletor de lixo pode
// mover a pilha durante uma chamada que volta para o Go (a janela recebe
// mensagens dentro de DispatchMessage) e o Windows ficaria com um endereço velho.
//
//go:uintptrescapes
func call(proc *windows.LazyProc, args ...uintptr) uintptr {
	result, _, _ := proc.Call(args...)
	return result
}

func copyUTF16(target []uint16, text string) {
	encoded, _ := windows.UTF16FromString(text)
	if len(encoded) > len(target) {
		encoded = encoded[:len(target)]
		encoded[len(encoded)-1] = 0
	}
	copy(target, encoded)
}

func keyDown(key int) bool {
	return int16(call(procGetKeyState, uintptr(key)))&-0x8000 != 0
}

// comCall chama o método de índice index na tabela virtual de um objeto COM.
//
//go:uintptrescapes
func comCall(object uintptr, index int, args ...uintptr) uintptr {
	vtable := *(*uintptr)(pointerFrom(object))
	method := *(*uintptr)(pointerFrom(vtable + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	result, _, _ := syscall.SyscallN(method, append([]uintptr{object}, args...)...)
	return result
}

// pointerFrom converte um endereço recebido do Windows (lParam, memória
// global, objetos COM) em ponteiro. A memória pertence ao sistema, não ao Go.
func pointerFrom(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}
