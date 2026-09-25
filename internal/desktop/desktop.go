// Package desktop is the small amount of desktop integration the programs
// have: opening the browser, naming the console window, stopping on Ctrl+C
// or a closed window, and on Windows a notification-area icon with Open and
// Shut Down entries, so a running program is visible and can be closed like
// any other.
package desktop

import (
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

// Options describes the notification-area icon shown by Tray.
type Options struct {
	Tooltip   string // shown when hovering the icon; at most 127 characters
	Icon      []byte // the icon in .ico format
	OpenURL   string // when set, "Open TAM" and a left click open this address
	QuitLabel string // the menu entry that stops the program

	// Quit is called when the quit entry is chosen or the system closes the
	// icon (log off, taskkill). It must stop the program, which closes the
	// done channel given to Tray.
	Quit func()
}

// OpenBrowser asks the operating system to open url in the default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// StopOnSignal calls stop on Ctrl+C, on SIGTERM, and on Windows when the
// console window is closed or the user logs off. It returns once done is
// closed, so it is normally run in its own goroutine.
func StopOnSignal(stop func(), done <-chan struct{}) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	select {
	case s := <-sigs:
		log.Printf("%v: stopping", s)
		stop()
	case <-done:
	}
}

func openURL(url string) {
	if err := OpenBrowser(url); err != nil {
		log.Printf("could not open the browser (%v); open %s yourself", err, url)
	}
}
