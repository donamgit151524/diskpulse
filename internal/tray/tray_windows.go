//go:build windows

package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCreateMutexW             = kernel32.NewProc("CreateMutexW")
	procGetModuleHandleW         = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procCreatePopupMenu          = user32.NewProc("CreatePopupMenu")
	procDestroyMenu              = user32.NewProc("DestroyMenu")
	procAppendMenuW              = user32.NewProc("AppendMenuW")
	procTrackPopupMenu           = user32.NewProc("TrackPopupMenu")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procLoadIconW                = user32.NewProc("LoadIconW")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
	procRegisterWindowMessageW   = user32.NewProc("RegisterWindowMessageW")
	procShellNotifyIconW         = shell32.NewProc("Shell_NotifyIconW")
)

const (
	errorAlreadyExists = 183

	wmNull          = 0x0000
	wmDestroy       = 0x0002
	wmCommand       = 0x0111
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmContextMenu   = 0x007B
	wmApp           = 0x8000
	wmTrayCallback  = wmApp + 1
	wmSyncState     = wmApp + 2
	wmShowToast     = wmApp + 3
	wmQuitApp       = wmApp + 4

	ninBalloonUserClick = 0x0405

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo    = 0x00000001
	niifWarning = 0x00000002
	niifError   = 0x00000003

	notifyIconVersion4 = 4

	mfString    = 0x00000000
	mfChecked   = 0x00000008
	mfGrayed    = 0x00000001
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020

	cmdOpenDashboard = 1001
	cmdScanNow       = 1002
	cmdTogglePause   = 1003
	cmdToggleAuto    = 1004
	cmdExit          = 1005
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type notifyIconData struct {
	CbSize            uint32
	HWnd              uintptr
	UID               uint32
	UFlags            uint32
	UCallbackMessage  uint32
	HIcon             uintptr
	SzTip             [128]uint16
	DwState           uint32
	DwStateMask       uint32
	SzInfo            [256]uint16
	UTimeoutOrVersion uint32
	SzInfoTitle       [64]uint16
	DwInfoFlags       uint32
	GuidItem          [16]byte
	HBalloonIcon      uintptr
}

// State reflects current status displayed by the tray icon and context menu.
type State struct {
	Tooltip   string
	Severity  string // "normal", "warning", "critical"
	Scanning  bool
	Paused    bool
	AutoStart bool
}

// Callbacks connects tray menu actions to application orchestration.
type Callbacks struct {
	OnOpenDashboard   func()
	OnScanNow         func()
	OnTogglePause     func()
	OnToggleAutoStart func()
	OnExit            func()
}

type toastPayload struct {
	title    string
	body     string
	severity string
}

// Tray manages the Windows system tray icon and native balloon/toast notifications.
type Tray struct {
	cb               Callbacks
	mu               sync.Mutex
	hwnd             uintptr
	state            State
	toastQueue       []toastPayload
	icons            map[string]uintptr
	wmTaskbarCreated uint32
}

// AcquireSingleInstance creates a named Windows mutex; returns alreadyRunning=true if another instance holds it.
func AcquireSingleInstance(mutexName string) (handle uintptr, alreadyRunning bool, err error) {
	ptr, err := syscall.UTF16PtrFromString(mutexName)
	if err != nil {
		return 0, false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if h == 0 {
		return 0, false, callErr
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		return h, true, nil
	}
	return h, false, nil
}

// New creates a Tray instance. Call Run() on a dedicated goroutine or main thread.
func New(initial State, cb Callbacks) *Tray {
	if initial.Tooltip == "" {
		initial.Tooltip = "DiskPulse — Đang theo dõi dung lượng"
	}
	if initial.Severity == "" {
		initial.Severity = "normal"
	}
	return &Tray{
		cb:    cb,
		state: initial,
		icons: make(map[string]uintptr),
	}
}

// UpdateState safely updates tray tooltip, status icon color, and menu flags.
func (t *Tray) UpdateState(s State) {
	t.mu.Lock()
	if s.Tooltip == "" {
		s.Tooltip = "DiskPulse"
	}
	if s.Severity == "" {
		s.Severity = "normal"
	}
	t.state = s
	hwnd := t.hwnd
	t.mu.Unlock()

	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmSyncState, 0, 0)
	}
}

// ShowNotification displays a Windows notification balloon / toast from the tray icon.
func (t *Tray) ShowNotification(title, body, severity string) {
	t.mu.Lock()
	t.toastQueue = append(t.toastQueue, toastPayload{
		title:    title,
		body:     body,
		severity: severity,
	})
	hwnd := t.hwnd
	t.mu.Unlock()

	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmShowToast, 0, 0)
	}
}

// Stop requests the tray message loop to exit cleanly.
func (t *Tray) Stop() {
	t.mu.Lock()
	hwnd := t.hwnd
	t.mu.Unlock()
	if hwnd != 0 {
		_, _, _ = procPostMessageW.Call(hwnd, wmQuitApp, 0, 0)
	}
}

// Run starts the Win32 message loop and blocks until Exit is chosen or Stop is called.
func (t *Tray) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("DiskPulseTrayWindow")
	taskbarMsgName, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	wmTB, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarMsgName)))
	t.wmTaskbarCreated = uint32(wmTB)

	t.icons["normal"] = buildStatusIcon(color.NRGBA{R: 14, G: 165, B: 233, A: 255})   // Sky blue
	t.icons["warning"] = buildStatusIcon(color.NRGBA{R: 245, G: 158, B: 11, A: 255})  // Amber
	t.icons["critical"] = buildStatusIcon(color.NRGBA{R: 239, G: 68, B: 68, A: 255})  // Red
	defer func() {
		for _, h := range t.icons {
			if h != 0 {
				_, _, _ = procDestroyIcon.Call(h)
			}
		}
	}()

	wndProc := syscall.NewCallback(func(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
		return t.handleWndProc(hwnd, uMsg, wParam, lParam)
	})

	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   wndProc,
		HInstance:     hInst,
		LpszClassName: className,
	}
	atom, _, regErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return regErr
	}

	titlePtr, _ := syscall.UTF16PtrFromString("DiskPulseTray")
	hwnd, _, winErr := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		0,
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return winErr
	}

	t.mu.Lock()
	t.hwnd = hwnd
	t.mu.Unlock()

	t.addTrayIcon(hwnd)
	defer t.deleteTrayIcon(hwnd)

	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	return nil
}

func (t *Tray) handleWndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	if t.wmTaskbarCreated != 0 && uMsg == t.wmTaskbarCreated {
		t.addTrayIcon(hwnd)
		return 0
	}

	switch uMsg {
	case wmTrayCallback:
		event := uint32(lParam & 0xFFFF)
		switch event {
		case wmLButtonUp, wmLButtonDblClk, ninBalloonUserClick:
			if t.cb.OnOpenDashboard != nil {
				go t.cb.OnOpenDashboard()
			}
		case wmRButtonUp, wmContextMenu:
			t.showContextMenu(hwnd)
		}
		return 0

	case wmSyncState:
		t.syncTrayIcon(hwnd)
		return 0

	case wmShowToast:
		t.flushToastQueue(hwnd)
		return 0

	case wmQuitApp:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0

	case wmCommand:
		cmdID := int(wParam & 0xFFFF)
		switch cmdID {
		case cmdOpenDashboard:
			if t.cb.OnOpenDashboard != nil {
				go t.cb.OnOpenDashboard()
			}
		case cmdScanNow:
			if t.cb.OnScanNow != nil {
				go t.cb.OnScanNow()
			}
		case cmdTogglePause:
			if t.cb.OnTogglePause != nil {
				go t.cb.OnTogglePause()
			}
		case cmdToggleAuto:
			if t.cb.OnToggleAutoStart != nil {
				go t.cb.OnToggleAutoStart()
			}
		case cmdExit:
			if t.cb.OnExit != nil {
				go t.cb.OnExit()
			}
			_, _, _ = procDestroyWindow.Call(hwnd)
		}
		return 0

	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
}

func (t *Tray) addTrayIcon(hwnd uintptr) {
	t.mu.Lock()
	state := t.state
	t.mu.Unlock()

	nid := t.baseNID(hwnd, state)
	nid.UFlags = nifMessage | nifIcon | nifTip
	_, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	nid.UTimeoutOrVersion = notifyIconVersion4
	_, _, _ = procShellNotifyIconW.Call(nimSetVersion, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) syncTrayIcon(hwnd uintptr) {
	t.mu.Lock()
	state := t.state
	t.mu.Unlock()

	nid := t.baseNID(hwnd, state)
	nid.UFlags = nifIcon | nifTip
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) flushToastQueue(hwnd uintptr) {
	t.mu.Lock()
	if len(t.toastQueue) == 0 {
		t.mu.Unlock()
		return
	}
	toast := t.toastQueue[0]
	t.toastQueue = t.toastQueue[1:]
	state := t.state
	hasMore := len(t.toastQueue) > 0
	t.mu.Unlock()

	nid := t.baseNID(hwnd, state)
	nid.UFlags = nifInfo
	copyUTF16(nid.SzInfoTitle[:], toast.title)
	copyUTF16(nid.SzInfo[:], toast.body)
	switch toast.severity {
	case "critical":
		nid.DwInfoFlags = niifError
	case "warning":
		nid.DwInfoFlags = niifWarning
	default:
		nid.DwInfoFlags = niifInfo
	}
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))

	if hasMore {
		_, _, _ = procPostMessageW.Call(hwnd, wmShowToast, 0, 0)
	}
}

func (t *Tray) deleteTrayIcon(hwnd uintptr) {
	nid := notifyIconData{
		CbSize: uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:   hwnd,
		UID:    1,
	}
	_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (t *Tray) baseNID(hwnd uintptr, state State) notifyIconData {
	hIcon := t.icons[state.Severity]
	if hIcon == 0 {
		hIcon = t.icons["normal"]
	}
	nid := notifyIconData{
		CbSize:           uint32(unsafe.Sizeof(notifyIconData{})),
		HWnd:             hwnd,
		UID:              1,
		UCallbackMessage: wmTrayCallback,
		HIcon:            hIcon,
	}
	copyUTF16(nid.SzTip[:], state.Tooltip)
	return nid
}

func (t *Tray) showContextMenu(hwnd uintptr) {
	t.mu.Lock()
	state := t.state
	t.mu.Unlock()

	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	appendItem := func(flags uint32, id uintptr, text string) {
		var ptr uintptr
		if text != "" {
			p, _ := syscall.UTF16PtrFromString(text)
			ptr = uintptr(unsafe.Pointer(p))
		}
		_, _, _ = procAppendMenuW.Call(menu, uintptr(flags), id, ptr)
	}

	appendItem(mfString, cmdOpenDashboard, "Mở Dashboard DiskPulse")
	if state.Scanning {
		appendItem(mfString|mfGrayed, cmdScanNow, "Đang quét ổ đĩa...")
	} else {
		appendItem(mfString, cmdScanNow, "Quét ngay bây giờ")
	}
	appendItem(mfSeparator, 0, "")

	pauseLabel := "Tạm dừng quét tự động"
	pauseFlags := uint32(mfString)
	if state.Paused {
		pauseFlags |= mfChecked
	}
	appendItem(pauseFlags, cmdTogglePause, pauseLabel)

	autoFlags := uint32(mfString)
	if state.AutoStart {
		autoFlags |= mfChecked
	}
	appendItem(autoFlags, cmdToggleAuto, "Khởi động cùng Windows")

	appendItem(mfSeparator, 0, "")
	appendItem(mfString, cmdExit, "Thoát DiskPulse")

	var pt point
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(hwnd)
	_, _, _ = procTrackPopupMenu.Call(
		menu,
		tpmRightButton|tpmBottomAlign,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		hwnd,
		0,
	)
	_, _, _ = procPostMessageW.Call(hwnd, wmNull, 0, 0)
}

func copyUTF16(dst []uint16, s string) {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		return
	}
	if len(u) > len(dst) {
		copy(dst, u[:len(dst)-1])
		dst[len(dst)-1] = 0
		return
	}
	copy(dst, u)
}

func buildStatusIcon(accent color.NRGBA) uintptr {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	bg := color.NRGBA{R: 15, G: 23, B: 42, A: 255}
	ringBg := color.NRGBA{R: 51, G: 65, B: 85, A: 255}
	cx, cy := 15.5, 15.5

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := float64(x) - cx
			dy := float64(y) - cy
			dist := math.Hypot(dx, dy)
			if dist <= 15.0 {
				c := bg
				if dist >= 8.5 && dist <= 12.5 {
					angle := math.Atan2(dy, dx)
					if angle < 0.9 {
						c = accent
					} else {
						c = ringBg
					}
				} else if dist <= 4.2 {
					c = accent
				}
				img.SetNRGBA(x, y, c)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err == nil {
		raw := buf.Bytes()
		h, _, _ := procCreateIconFromResourceEx.Call(
			uintptr(unsafe.Pointer(&raw[0])),
			uintptr(len(raw)),
			1,
			0x00030000,
			size,
			size,
			0,
		)
		if h != 0 {
			return h
		}
	}

	// Fallback standard Windows application icon (IDI_APPLICATION = 32512).
	h, _, _ := procLoadIconW.Call(0, 32512)
	return h
}
