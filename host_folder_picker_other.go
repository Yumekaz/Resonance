//go:build !windows

package main

import "os/exec"

func nativeFolderPickerAvailable() bool         { return false }
func configurePickerProcess(*exec.Cmd)          {}
func nativeChooseFolder() (folderChoice, error) { return folderChoice{}, errPickerUnsupported }
