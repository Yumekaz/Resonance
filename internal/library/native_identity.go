package library

import (
	"os"

	"resonance/internal/storage"
)

type NativeIdentityProvider interface {
	FromOpenFile(*os.File) (storage.NativeIdentity, bool, error)
}

type noNativeIdentityProvider struct{}

func (noNativeIdentityProvider) FromOpenFile(*os.File) (storage.NativeIdentity, bool, error) {
	return storage.NativeIdentity{}, false, nil
}

type FakeNativeIdentityProvider func(*os.File) (storage.NativeIdentity, bool, error)

func (f FakeNativeIdentityProvider) FromOpenFile(file *os.File) (storage.NativeIdentity, bool, error) {
	return f(file)
}

// NativeIdentityFromOpenFile reports optional platform evidence from the
// already-confined media handle. Playback never uses it as a path authority.
func NativeIdentityFromOpenFile(file *os.File) (storage.NativeIdentity, bool, error) {
	return systemNativeIdentityProvider().FromOpenFile(file)
}
