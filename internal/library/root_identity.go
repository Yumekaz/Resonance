package library

import (
	"os"

	"resonance/internal/storage"
)

// CaptureRootIdentity opens the enrolled directory through os.Root and reads
// identity evidence from that confined directory handle. The boolean is false
// when the platform cannot provide a usable persistent identity.
func CaptureRootIdentity(path string) (storage.NativeIdentity, bool, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	defer root.Close()
	identityA, ok, err := rootIdentityFromOpenRoot(root)
	if err != nil || !ok {
		return identityA, ok, err
	}
	startInfo, err := root.Stat(".")
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	startPathInfo, err := os.Stat(path)
	if err != nil || !os.SameFile(startInfo, startPathInfo) {
		return storage.NativeIdentity{}, false, storage.ErrRootIdentityMismatch
	}
	endRoot, err := os.OpenRoot(path)
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	defer endRoot.Close()
	identityB, ok, err := rootIdentityFromOpenRoot(endRoot)
	if err != nil || !ok {
		return identityB, ok, err
	}
	endInfo, err := endRoot.Stat(".")
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	endPathInfo, err := os.Stat(path)
	if err != nil || !os.SameFile(endInfo, endPathInfo) || !storage.RootIdentityEqual(&identityA, &identityB) || !os.SameFile(startInfo, endInfo) {
		return storage.NativeIdentity{}, false, storage.ErrRootIdentityMismatch
	}
	return identityA, true, nil
}

func rootIdentityFromOpenRoot(root *os.Root) (storage.NativeIdentity, bool, error) {
	if root == nil {
		return storage.NativeIdentity{}, false, os.ErrInvalid
	}
	file, err := root.Open(".")
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	if !info.IsDir() {
		return storage.NativeIdentity{}, false, os.ErrInvalid
	}
	identity, ok, err := platformRootIdentity(file)
	if err != nil || !ok {
		return identity, ok, err
	}
	if !hasStrongBirthEvidence(identity.BirthToken) {
		return storage.NativeIdentity{}, false, nil
	}
	return identity, true, nil
}

func hasStrongBirthEvidence(token []byte) bool {
	for _, value := range token {
		if value != 0 {
			return true
		}
	}
	return false
}
