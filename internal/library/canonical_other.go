//go:build !windows

package library

func canonicalPathByHandle(path string) (string, error) { return path, nil }
