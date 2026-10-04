//go:build windows

package main

import (
	"errors"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var pickerOLE = windows.NewLazySystemDLL("ole32.dll")
var pickerCoCreate = pickerOLE.NewProc("CoCreateInstance")
var pickerCoUninitialize = pickerOLE.NewProc("CoUninitialize")
var pickerCoInitialize = pickerOLE.NewProc("CoInitializeEx")

// IUnknown (3 slots), IModalWindow::Show, then IFileDialog in SDK order.
// Native pointers and allocated shell strings never leave this helper process.
type nativeFolderDialog struct{ methods *[27]uintptr }
type nativeFolderItem struct{ methods *[8]uintptr }

func nativeFolderPickerAvailable() bool {
	var session uint32
	return windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session) == nil && session != 0
}
func configurePickerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	command.WaitDelay = time.Second
}
func pickerHRESULT(result uintptr) error {
	if int32(result) < 0 {
		return errors.New("Windows folder dialog failed")
	}
	return nil
}
func newNativeFolderDialog() (*nativeFolderDialog, error) {
	class := windows.GUID{Data1: 0xdc1c5a9c, Data2: 0xe88a, Data3: 0x4dde, Data4: [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	iid := windows.GUID{Data1: 0xd57c7288, Data2: 0xd4ad, Data3: 0x4768, Data4: [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	var dialog *nativeFolderDialog
	result, _, _ := pickerCoCreate.Call(uintptr(unsafe.Pointer(&class)), 0, 1, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&dialog)))
	if err := pickerHRESULT(result); err != nil {
		return nil, err
	}
	if dialog == nil {
		return nil, errors.New("Windows folder dialog unavailable")
	}
	var options uint32
	result, _, _ = syscall.SyscallN(dialog.methods[10], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(&options)))
	if err := pickerHRESULT(result); err != nil {
		dialog.release()
		return nil, err
	}
	// Pick one existing filesystem folder, preserve cwd, don't add recent items.
	options = (options &^ 0x200) | 0x20 | 0x40 | 0x800 | 0x8 | 0x2000000
	result, _, _ = syscall.SyscallN(dialog.methods[9], uintptr(unsafe.Pointer(dialog)), uintptr(options))
	if err := pickerHRESULT(result); err != nil {
		dialog.release()
		return nil, err
	}
	for _, text := range []struct {
		slot  int
		value string
	}{{17, "Choose a music folder — Resonance"}, {18, "Choose folder"}} {
		title, _ := windows.UTF16PtrFromString(text.value)
		result, _, _ = syscall.SyscallN(dialog.methods[text.slot], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(title)))
		if err := pickerHRESULT(result); err != nil {
			dialog.release()
			return nil, err
		}
	}
	return dialog, nil
}
func (dialog *nativeFolderDialog) release() {
	syscall.SyscallN(dialog.methods[2], uintptr(unsafe.Pointer(dialog)))
}
func nativeChooseFolder() (folderChoice, error) {
	// COM's STA and all interface calls must stay on one OS thread. The parent
	// kills this isolated process on timeout/disconnect, closing its dialog too.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized, _, _ := pickerCoInitialize.Call(0, 0x6)
	if err := pickerHRESULT(initialized); err != nil {
		return folderChoice{}, err
	}
	defer pickerCoUninitialize.Call()
	dialog, err := newNativeFolderDialog()
	if err != nil {
		return folderChoice{}, err
	}
	defer dialog.release()
	result, _, _ := syscall.SyscallN(dialog.methods[3], uintptr(unsafe.Pointer(dialog)), 0)
	if uint32(result) == 0x800704c7 {
		return folderChoice{Cancelled: true}, nil
	}
	if err := pickerHRESULT(result); err != nil {
		return folderChoice{}, err
	}
	var item *nativeFolderItem
	result, _, _ = syscall.SyscallN(dialog.methods[20], uintptr(unsafe.Pointer(dialog)), uintptr(unsafe.Pointer(&item)))
	if err := pickerHRESULT(result); err != nil {
		return folderChoice{}, err
	}
	if item == nil {
		return folderChoice{}, errors.New("no folder selected")
	}
	defer syscall.SyscallN(item.methods[2], uintptr(unsafe.Pointer(item)))
	var path *uint16
	result, _, _ = syscall.SyscallN(item.methods[5], uintptr(unsafe.Pointer(item)), 0x80058000, uintptr(unsafe.Pointer(&path)))
	if err := pickerHRESULT(result); err != nil {
		return folderChoice{}, err
	}
	if path == nil {
		return folderChoice{}, errors.New("no filesystem path")
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(path))
	return folderChoice{Path: windows.UTF16PtrToString(path)}, nil
}
