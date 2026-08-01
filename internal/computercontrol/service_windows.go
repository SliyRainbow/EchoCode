//go:build windows

package computercontrol

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	gdi32                      = windows.NewLazySystemDLL("gdi32.dll")
	procEnumWindows            = user32.NewProc("EnumWindows")
	procIsWindow               = user32.NewProc("IsWindow")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
	procIsIconic               = user32.NewProc("IsIconic")
	procGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	procShowWindow             = user32.NewProc("ShowWindow")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procSetCursorPos           = user32.NewProc("SetCursorPos")
	procMouseEvent             = user32.NewProc("mouse_event")
	procSendInput              = user32.NewProc("SendInput")
	procGetWindowDC            = user32.NewProc("GetWindowDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procPrintWindow            = user32.NewProc("PrintWindow")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

const (
	swRestore           = 9
	mouseLeftDown       = 0x0002
	mouseLeftUp         = 0x0004
	inputKeyboard       = 1
	keyEventKeyUp       = 0x0002
	keyEventUnicode     = 0x0004
	processQueryLimited = 0x1000
	srccopy             = 0x00CC0020
	captureBLT          = 0x40000000
	dibRGBColors        = 0
)

type rect struct{ Left, Top, Right, Bottom int32 }

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

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type keyboardInput struct {
	VirtualKey uint16
	ScanCode   uint16
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
	Padding    [8]byte
}

type input struct {
	Type     uint32
	Padding  uint32
	Keyboard keyboardInput
}

func (*Service) ListWindows(ctx context.Context) ([]Window, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	foreground, _, _ := procGetForegroundWindow.Call()
	windowsList := make([]Window, 0, 16)
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if ctx.Err() != nil {
			return 0
		}
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		title := windowTitle(hwnd)
		if strings.TrimSpace(title) == "" {
			return 1
		}
		bounds, ok := windowRect(hwnd)
		if !ok || bounds.Right <= bounds.Left || bounds.Bottom <= bounds.Top {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		iconic, _, _ := procIsIconic.Call(hwnd)
		windowsList = append(windowsList, Window{
			ID: windowID(hwnd), Title: title, ProcessName: processName(pid), PID: pid,
			X: int(bounds.Left), Y: int(bounds.Top), Width: int(bounds.Right - bounds.Left), Height: int(bounds.Bottom - bounds.Top),
			Minimized: iconic != 0, Foreground: hwnd == foreground,
		})
		return 1
	})
	result, _, callErr := procEnumWindows.Call(callback, 0)
	if result == 0 && callErr != windows.ERROR_SUCCESS {
		return nil, fmt.Errorf("枚举窗口失败: %w", callErr)
	}
	sort.SliceStable(windowsList, func(i, j int) bool {
		if windowsList[i].Foreground != windowsList[j].Foreground {
			return windowsList[i].Foreground
		}
		return strings.ToLower(windowsList[i].Title) < strings.ToLower(windowsList[j].Title)
	})
	return windowsList, ctx.Err()
}

func (*Service) FocusWindow(ctx context.Context, id string) error {
	hwnd, err := validWindow(ctx, id)
	if err != nil {
		return err
	}
	iconic, _, _ := procIsIconic.Call(hwnd)
	if iconic != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	ok, _, callErr := procSetForegroundWindow.Call(hwnd)
	if ok == 0 {
		return fmt.Errorf("无法聚焦窗口 %s: %w", id, callErr)
	}
	return nil
}

func (s *Service) ClickWindow(ctx context.Context, id string, x, y int) error {
	hwnd, err := validWindow(ctx, id)
	if err != nil {
		return err
	}
	bounds, ok := windowRect(hwnd)
	if !ok {
		return fmt.Errorf("无法读取窗口位置")
	}
	width, height := int(bounds.Right-bounds.Left), int(bounds.Bottom-bounds.Top)
	if x < 0 || y < 0 || x >= width || y >= height {
		return fmt.Errorf("点击坐标超出窗口范围: %d,%d（%dx%d）", x, y, width, height)
	}
	if err := s.FocusWindow(ctx, id); err != nil {
		return err
	}
	if ok, _, callErr := procSetCursorPos.Call(uintptr(int(bounds.Left)+x), uintptr(int(bounds.Top)+y)); ok == 0 {
		return fmt.Errorf("移动鼠标失败: %w", callErr)
	}
	procMouseEvent.Call(mouseLeftDown, 0, 0, 0, 0)
	procMouseEvent.Call(mouseLeftUp, 0, 0, 0, 0)
	return nil
}

func (s *Service) TypeText(ctx context.Context, id, text string) error {
	if text == "" {
		return nil
	}
	if err := s.FocusWindow(ctx, id); err != nil {
		return err
	}
	units := utf16.Encode([]rune(text))
	inputs := make([]input, 0, len(units)*2)
	for _, unit := range units {
		inputs = append(inputs,
			keyboardEvent(0, unit, keyEventUnicode),
			keyboardEvent(0, unit, keyEventUnicode|keyEventKeyUp),
		)
	}
	return sendInputs(inputs)
}

func (s *Service) PressKey(ctx context.Context, id, key string, ctrl, alt, shift bool) error {
	if err := s.FocusWindow(ctx, id); err != nil {
		return err
	}
	vk, err := virtualKey(key)
	if err != nil {
		return err
	}
	modifiers := make([]uint16, 0, 3)
	if ctrl {
		modifiers = append(modifiers, 0x11)
	}
	if alt {
		modifiers = append(modifiers, 0x12)
	}
	if shift {
		modifiers = append(modifiers, 0x10)
	}
	inputs := make([]input, 0, len(modifiers)*2+2)
	for _, modifier := range modifiers {
		inputs = append(inputs, keyboardEvent(modifier, 0, 0))
	}
	inputs = append(inputs, keyboardEvent(vk, 0, 0), keyboardEvent(vk, 0, keyEventKeyUp))
	for index := len(modifiers) - 1; index >= 0; index-- {
		inputs = append(inputs, keyboardEvent(modifiers[index], 0, keyEventKeyUp))
	}
	return sendInputs(inputs)
}

func (*Service) ScreenshotWindow(ctx context.Context, id, outputPath string) (string, error) {
	hwnd, err := validWindow(ctx, id)
	if err != nil {
		return "", err
	}
	bounds, ok := windowRect(hwnd)
	if !ok {
		return "", fmt.Errorf("无法读取窗口位置")
	}
	width, height := int(bounds.Right-bounds.Left), int(bounds.Bottom-bounds.Top)
	if width < 1 || height < 1 || width > 10000 || height > 10000 {
		return "", fmt.Errorf("窗口尺寸无效: %dx%d", width, height)
	}
	imageData, err := captureWindow(hwnd, width, height)
	if err != nil {
		return "", err
	}
	outputPath, err = filepath.Abs(filepath.Clean(outputPath))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}
	file, err := os.Create(outputPath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := png.Encode(file, imageData); err != nil {
		return "", err
	}
	return outputPath, nil
}

func validWindow(ctx context.Context, id string) (uintptr, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	hwnd, err := parseWindowID(id)
	if err != nil {
		return 0, err
	}
	ok, _, _ := procIsWindow.Call(hwnd)
	if ok == 0 {
		return 0, fmt.Errorf("窗口不存在或已经关闭: %s", id)
	}
	return hwnd, nil
}

func parseWindowID(id string) (uintptr, error) {
	value := strings.TrimSpace(strings.ToLower(id))
	value = strings.TrimPrefix(value, "0x")
	parsed, err := strconv.ParseUint(value, 16, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("窗口 ID 无效: %s", id)
	}
	return uintptr(parsed), nil
}

func windowID(hwnd uintptr) string { return fmt.Sprintf("0x%X", hwnd) }

func windowTitle(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buffer := make([]uint16, length+1)
	read, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if read == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:read])
}

func windowRect(hwnd uintptr) (rect, bool) {
	var bounds rect
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&bounds)))
	return bounds, ok != 0
}

func processName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	handle, err := windows.OpenProcess(processQueryLimited, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buffer[:size]))
}

func keyboardEvent(vk, scan uint16, flags uint32) input {
	return input{Type: inputKeyboard, Keyboard: keyboardInput{VirtualKey: vk, ScanCode: scan, Flags: flags}}
}

func sendInputs(inputs []input) error {
	if len(inputs) == 0 {
		return nil
	}
	sent, _, callErr := procSendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(input{}))
	if sent != uintptr(len(inputs)) {
		return fmt.Errorf("键盘输入只发送了 %d/%d 项: %w", sent, len(inputs), callErr)
	}
	return nil
}

func virtualKey(key string) (uint16, error) {
	value := strings.ToUpper(strings.TrimSpace(key))
	keys := map[string]uint16{
		"ENTER": 0x0D, "TAB": 0x09, "ESC": 0x1B, "ESCAPE": 0x1B, "BACKSPACE": 0x08,
		"DELETE": 0x2E, "SPACE": 0x20, "LEFT": 0x25, "UP": 0x26, "RIGHT": 0x27, "DOWN": 0x28,
		"HOME": 0x24, "END": 0x23, "PAGEUP": 0x21, "PAGEDOWN": 0x22,
	}
	if vk, ok := keys[value]; ok {
		return vk, nil
	}
	if len(value) == 1 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= '0' && value[0] <= '9')) {
		return uint16(value[0]), nil
	}
	if strings.HasPrefix(value, "F") {
		if number, err := strconv.Atoi(strings.TrimPrefix(value, "F")); err == nil && number >= 1 && number <= 12 {
			return uint16(0x70 + number - 1), nil
		}
	}
	return 0, fmt.Errorf("不支持的按键: %s", key)
}

func captureWindow(hwnd uintptr, width, height int) (*image.NRGBA, error) {
	windowDC, _, callErr := procGetWindowDC.Call(hwnd)
	if windowDC == 0 {
		return nil, fmt.Errorf("获取窗口画面失败: %w", callErr)
	}
	defer procReleaseDC.Call(hwnd, windowDC)
	memoryDC, _, callErr := procCreateCompatibleDC.Call(windowDC)
	if memoryDC == 0 {
		return nil, fmt.Errorf("创建截图缓冲区失败: %w", callErr)
	}
	defer procDeleteDC.Call(memoryDC)
	bitmap, _, callErr := procCreateCompatibleBitmap.Call(windowDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, fmt.Errorf("创建截图位图失败: %w", callErr)
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(memoryDC, bitmap)
	defer procSelectObject.Call(memoryDC, previous)
	printed, _, _ := procPrintWindow.Call(hwnd, memoryDC, 2)
	if printed == 0 {
		copied, _, callErr := procBitBlt.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), windowDC, 0, 0, srccopy|captureBLT)
		if copied == 0 {
			return nil, fmt.Errorf("截取窗口失败: %w", callErr)
		}
	}
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(width), Height: -int32(height),
		Planes: 1, BitCount: 32,
	}}
	pixels := make([]byte, width*height*4)
	rows, _, callErr := procGetDIBits.Call(memoryDC, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), dibRGBColors)
	if rows == 0 {
		return nil, fmt.Errorf("读取窗口位图失败: %w", callErr)
	}
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for offset := 0; offset < len(pixels); offset += 4 {
		result.SetNRGBA((offset/4)%width, (offset/4)/width, color.NRGBA{R: pixels[offset+2], G: pixels[offset+1], B: pixels[offset], A: 255})
	}
	return result, nil
}
