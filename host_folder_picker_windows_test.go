//go:build windows

package main

import (
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// Real COM construction/configuration without showing UI or touching a folder.
func TestWindowsNativeFolderDialogConfiguration(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result, _, _ := pickerCoInitialize.Call(0, 0x6)
	if err := pickerHRESULT(result); err != nil {
		t.Fatal(err)
	}
	defer pickerCoUninitialize.Call()
	dialog, err := newNativeFolderDialog()
	if err != nil {
		t.Fatal(err)
	}
	defer dialog.release()
	var options uint32
	result, _, _ = syscall.SyscallN(dialog.methods[10], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(&options)))
	if err := pickerHRESULT(result); err != nil {
		t.Fatal(err)
	}
	const required = 0x20 | 0x40 | 0x800 | 0x8 | 0x2000000
	if options&required != required || options&0x200 != 0 {
		t.Fatalf("unexpected folder options %x", options)
	}
}
