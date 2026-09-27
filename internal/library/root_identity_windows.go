//go:build windows

package library

import (
	"os"

	"resonance/internal/storage"
)

func platformRootIdentity(file *os.File) (storage.NativeIdentity, bool, error) {
	return (windowsNativeIdentityProvider{}).FromOpenFile(file)
}
