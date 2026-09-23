//go:build !windows

package library

func systemNativeIdentityProvider() NativeIdentityProvider {
	return noNativeIdentityProvider{}
}
