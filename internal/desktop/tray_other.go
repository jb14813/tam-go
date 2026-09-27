//go:build !windows

package desktop

// TraySupported reports whether Tray shows an icon on this platform.
const TraySupported = false

// Tray has no icon to show here; it returns once done is closed.
func Tray(o Options, done <-chan struct{}) {
	<-done
}

// SetConsoleTitle does nothing here.
func SetConsoleTitle(title string) {}
