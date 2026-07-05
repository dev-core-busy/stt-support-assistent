//go:build windows

package main

import (
	"fmt"
	"fyne.io/fyne/v2"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	procSetWindowPlacement = user32.NewProc("SetWindowPlacement")
	procGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	procFindWindowW        = user32.NewProc("FindWindowW")

	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

// setWindowSquare schaltet die Fensterrahmen-Ecken zwischen eckig (square=true)
// und abgerundet um. Wirkt nur unter Windows 11 (DWM); ältere Windows ignorieren
// das Attribut (Fenster sind dort ohnehin eckig). Mit kleiner Verzögerung, damit
// das Fenster (und sein HWND) bereits existiert.
func setWindowSquare(w fyne.Window, square bool) {
	time.AfterFunc(200*time.Millisecond, func() {
		hwnd := getHWND(w.Title())
		if hwnd == 0 {
			return
		}
		const dwmwaWindowCornerPreference = 33
		var pref int32 = 2 // DWMWCP_ROUND
		if square {
			pref = 1 // DWMWCP_DONOTROUND
		}
		procDwmSetWindowAttribute.Call(
			hwnd,
			uintptr(dwmwaWindowCornerPreference),
			uintptr(unsafe.Pointer(&pref)),
			uintptr(4),
		)
	})
}

type point struct {
	X, Y int32
}

type rect struct {
	Left, Top, Right, Bottom int32
}

type windowPlacement struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    point
	MaxPosition    point
	NormalPosition rect
}

func getHWND(title string) uintptr {
	tPtr, _ := syscall.UTF16PtrFromString(title)
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(tPtr)))
	return hwnd
}

func saveWindowPosition(w fyne.Window) {
	hwnd := getHWND(w.Title())
	if hwnd != 0 {
		var wp windowPlacement
		wp.Length = uint32(unsafe.Sizeof(wp))
		ret, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
		if ret != 0 {
			config.PhysX = wp.NormalPosition.Left
			config.PhysY = wp.NormalPosition.Top
			config.PhysWidth = wp.NormalPosition.Right - wp.NormalPosition.Left
			config.PhysHeight = wp.NormalPosition.Bottom - wp.NormalPosition.Top
			config.WinShowCmd = int32(wp.ShowCmd)
		}
	}
}

func restoreWindowPosition(w fyne.Window) {
	// Wir nutzen eine kleine Verzögerung, um sicherzustellen, dass Fyne
	// seine eigenen Layout-Initialisierungen abgeschlossen hat.
	time.AfterFunc(150*time.Millisecond, func() {
		hwnd := getHWND(w.Title())
		if hwnd != 0 {
			var wp windowPlacement
			wp.Length = uint32(unsafe.Sizeof(wp))

			// Aktuellen Zustand holen
			procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))

			// Physikalische Bildschirmpixel direkt anwenden
			wp.ShowCmd = uint32(config.WinShowCmd)
			wp.NormalPosition.Left = config.PhysX
			wp.NormalPosition.Top = config.PhysY
			wp.NormalPosition.Right = config.PhysX + config.PhysWidth
			wp.NormalPosition.Bottom = config.PhysY + config.PhysHeight

			procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
		}
	})
}

func moveWindowNear(child fyne.Window, parent fyne.Window) {
	time.AfterFunc(150*time.Millisecond, func() {
		mainHwnd := getHWND(parent.Title())
		childHwnd := getHWND(child.Title())
		if mainHwnd != 0 && childHwnd != 0 {
			var wpMain windowPlacement
			wpMain.Length = uint32(unsafe.Sizeof(wpMain))
			procGetWindowPlacement.Call(mainHwnd, uintptr(unsafe.Pointer(&wpMain)))

			var wpChild windowPlacement
			wpChild.Length = uint32(unsafe.Sizeof(wpChild))
			procGetWindowPlacement.Call(childHwnd, uintptr(unsafe.Pointer(&wpChild)))

			width := wpChild.NormalPosition.Right - wpChild.NormalPosition.Left
			height := wpChild.NormalPosition.Bottom - wpChild.NormalPosition.Top

			wpChild.NormalPosition.Left = wpMain.NormalPosition.Left + 80
			wpChild.NormalPosition.Top = wpMain.NormalPosition.Top + 80
			wpChild.NormalPosition.Right = wpChild.NormalPosition.Left + width
			wpChild.NormalPosition.Bottom = wpChild.NormalPosition.Top + height

			procSetWindowPlacement.Call(childHwnd, uintptr(unsafe.Pointer(&wpChild)))
		}
	})
}

var (
	procOpenClipboard            = user32.NewProc("OpenClipboard")
	procCloseClipboard           = user32.NewProc("CloseClipboard")
	procEmptyClipboard           = user32.NewProc("EmptyClipboard")
	procSetClipboardData         = user32.NewProc("SetClipboardData")
	procRegisterClipboardFormatW = user32.NewProc("RegisterClipboardFormatW")

	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
)

func copyToClipboardRich(plainText, markdown string) {
	re := regexp.MustCompile(`\*\*(.*?)\*\*`)
	htmlFrag := re.ReplaceAllString(markdown, "<b>$1</b>")
	htmlFrag = strings.ReplaceAll(htmlFrag, "\n", "<br>\n")

	hdr := "Version:0.9\r\nStartHTML:00000000\r\nEndHTML:00000000\r\nStartFragment:00000000\r\nEndFragment:00000000\r\n"
	prefix := "<html><body>\r\n<!--StartFragment-->"
	suffix := "<!--EndFragment-->\r\n</body>\r\n</html>"

	startHTML := len(hdr)
	startFrag := startHTML + len(prefix)
	endFrag := startFrag + len(htmlFrag)
	endHTML := endFrag + len(suffix)

	hdrFilled := fmt.Sprintf("Version:0.9\r\nStartHTML:%08d\r\nEndHTML:%08d\r\nStartFragment:%08d\r\nEndFragment:%08d\r\n", startHTML, endHTML, startFrag, endFrag)
	finalHTML := hdrFilled + prefix + htmlFrag + suffix

	formatName, _ := syscall.UTF16PtrFromString("HTML Format")
	htmlFmtId, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(formatName)))
	if htmlFmtId == 0 {
		return
	}

	htmlBytes := []byte(finalHTML)
	htmlBytes = append(htmlBytes, 0)
	hHtml, _, _ := procGlobalAlloc.Call(2, uintptr(len(htmlBytes)))
	if hHtml != 0 {
		ptr, _, _ := procGlobalLock.Call(hHtml)
		if ptr != 0 {
			copy(unsafe.Slice((*byte)(unsafe.Pointer(ptr)), len(htmlBytes)), htmlBytes)
			procGlobalUnlock.Call(hHtml)
		}
	}

	utf16Text, _ := syscall.UTF16FromString(plainText)
	hText, _, _ := procGlobalAlloc.Call(2, uintptr(len(utf16Text)*2))
	if hText != 0 {
		ptr, _, _ := procGlobalLock.Call(hText)
		if ptr != 0 {
			copy(unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(utf16Text)), utf16Text)
			procGlobalUnlock.Call(hText)
		}
	}

	ret, _, _ := procOpenClipboard.Call(0)
	if ret != 0 {
		procEmptyClipboard.Call()

		if hText != 0 {
			procSetClipboardData.Call(13, hText)
		}
		if hHtml != 0 {
			procSetClipboardData.Call(htmlFmtId, hHtml)
		}
		procCloseClipboard.Call()
	} else {
		if hText != 0 {
			procGlobalFree.Call(hText)
		}
		if hHtml != 0 {
			procGlobalFree.Call(hHtml)
		}
	}
}
