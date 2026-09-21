//go:build windows

package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var getFinalPathNameByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// canonicalPathByHandle collapses junction, short-name, case, mapped-drive, and
// other aliases that lexical cleaning and filepath.EvalSymlinks can leave on
// Windows. Enrollment compares this final handle path, never user spelling.
func canonicalPathByHandle(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := f.SyscallConn()
	if err != nil {
		return "", err
	}
	var final string
	var callErr error
	if err := raw.Control(func(handle uintptr) {
		size := uint32(256)
		for {
			buffer := make([]uint16, size)
			length, _, syscallErr := getFinalPathNameByHandle.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(size), 0)
			if length == 0 {
				if syscallErr != syscall.Errno(0) {
					callErr = syscallErr
				} else {
					callErr = errors.New("final path lookup failed")
				}
				return
			}
			if length < uintptr(size) {
				final = string(utf16.Decode(buffer[:length]))
				return
			}
			size = uint32(length) + 1
		}
	}); err != nil {
		return "", err
	}
	if callErr != nil {
		return "", callErr
	}
	switch {
	case strings.HasPrefix(final, `\\?\UNC\`):
		final = `\\` + final[len(`\\?\UNC\`):]
	case strings.HasPrefix(final, `\\?\`):
		final = final[len(`\\?\`):]
	}
	if final == "" {
		return "", errors.New("empty final path")
	}
	return filepath.Clean(final), nil
}
