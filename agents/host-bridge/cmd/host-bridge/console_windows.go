//go:build windows

package main

import "syscall"

// setWindowsUTF8Console switches the Windows console input/output code pages
// to UTF-8 (65001) so that Unicode block characters render correctly.
func setWindowsUTF8Console() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setConsoleCP := kernel32.NewProc("SetConsoleCP")
	setConsoleOutputCP := kernel32.NewProc("SetConsoleOutputCP")
	_, _, _ = setConsoleCP.Call(65001)
	_, _, _ = setConsoleOutputCP.Call(65001)
}
