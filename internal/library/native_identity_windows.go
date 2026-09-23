//go:build windows

package library

import (
	"encoding/binary"
	"fmt"
	"os"
	"syscall"

	"resonance/internal/storage"
)

type windowsNativeIdentityProvider struct{}

func (windowsNativeIdentityProvider) FromOpenFile(file *os.File) (storage.NativeIdentity, bool, error) {
	if file == nil {
		return storage.NativeIdentity{}, false, os.ErrInvalid
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &info); err != nil {
		return storage.NativeIdentity{}, false, err
	}
	var id [8]byte
	binary.BigEndian.PutUint32(id[:4], info.FileIndexHigh)
	binary.BigEndian.PutUint32(id[4:], info.FileIndexLow)
	var birth [8]byte
	binary.BigEndian.PutUint32(birth[:4], info.CreationTime.HighDateTime)
	binary.BigEndian.PutUint32(birth[4:], info.CreationTime.LowDateTime)
	return storage.NativeIdentity{
		Kind:       "windows_file_index",
		Scope:      fmt.Sprintf("windows-volume-%08x", info.VolumeSerialNumber),
		ID:         id[:],
		BirthToken: birth[:],
	}, true, nil
}

func systemNativeIdentityProvider() NativeIdentityProvider {
	return windowsNativeIdentityProvider{}
}
