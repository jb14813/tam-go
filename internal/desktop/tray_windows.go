package desktop

import (
	"os"
	"sync/atomic"
	"time"
	"unsafe"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
)

// TraySupported reports whether Tray shows an icon on this platform.
const TraySupported = true

// Tray shows the notification-area icon until done is closed. It runs the
// Windows message loop, so it must be called from the main goroutine, and
// it returns once done is closed.
//
// If the system closes the icon first (log off, taskkill), o.Quit is called
// so the program stops with it.
func Tray(o Options, done <-chan struct{}) {
	var ready atomic.Bool
	loopEnded := make(chan struct{})

	go func() {
		<-done
		systray.Quit()
		select {
		case <-loopEnded:
		case <-time.After(2 * time.Second):
			// The icon never came up, so there is no loop to end and
			// nothing left to serve.
			os.Exit(0)
		}
	}()

	systray.Run(func() {
		ready.Store(true)
		systray.SetIcon(o.Icon)
		systray.SetTooltip(o.Tooltip)

		var openCh <-chan struct{}
		if o.OpenURL != "" {
			open := systray.AddMenuItem("Open TAM", "Open the web app in your browser")
			openCh = open.ClickedCh
			systray.SetOnTapped(func() { openURL(o.OpenURL) })
			systray.AddSeparator()
		}
		quit := systray.AddMenuItem(o.QuitLabel, "Stop the program")

		go func() {
			for {
				select {
				case <-openCh:
					openURL(o.OpenURL)
				case <-quit.ClickedCh:
					o.Quit()
				case <-done:
					return
				}
			}
		}()
	}, nil)
	close(loopEnded)

	select {
	case <-done:
	default:
		if ready.Load() {
			// The icon was closed by the system while the program was still
			// running: treat it as a request to stop.
			o.Quit()
		}
	}
	<-done
}

var procSetConsoleTitle = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleTitleW")

// SetConsoleTitle names the console window, and with it the taskbar button
// the program gets when started by double-clicking it.
func SetConsoleTitle(title string) {
	p, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	procSetConsoleTitle.Call(uintptr(unsafe.Pointer(p)))
}
