package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	errPickerUnsupported = errors.New("native folder picker unavailable")
	errPickerBusy        = errors.New("folder picker already open")
)

type folderChoice struct {
	Path      string `json:"path,omitempty"`
	Cancelled bool   `json:"cancelled"`
}
type hostFolderPicker interface {
	Available() bool
	Pick(context.Context) (folderChoice, error)
}
type processFolderPicker struct {
	mu        sync.Mutex
	supported bool
	run       func(context.Context) ([]byte, error)
}

func newHostFolderPicker() hostFolderPicker {
	return &processFolderPicker{supported: nativeFolderPickerAvailable(), run: runFolderPickerProcess}
}
func (p *processFolderPicker) Available() bool { return p.supported }
func (p *processFolderPicker) Pick(ctx context.Context) (folderChoice, error) {
	if !p.supported {
		return folderChoice{}, errPickerUnsupported
	}
	if !p.mu.TryLock() {
		return folderChoice{}, errPickerBusy
	}
	defer p.mu.Unlock()
	data, err := p.run(ctx)
	if err != nil {
		return folderChoice{}, err
	}
	var choice folderChoice
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&choice) != nil {
		return folderChoice{}, errors.New("invalid picker result")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || choice.Cancelled && choice.Path != "" || !choice.Cancelled && !validAdminPath(choice.Path) {
		return folderChoice{}, errors.New("invalid picker result")
	}
	return choice, nil
}

type pickerOutput struct{ bytes.Buffer }

func (out *pickerOutput) Write(data []byte) (int, error) {
	if out.Len()+len(data) > 8192 {
		return 0, errors.New("picker output exceeds limit")
	}
	return out.Buffer.Write(data)
}
func runFolderPickerProcess(ctx context.Context) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// Only this executable and fixed arguments are launched. Browser input never
	// becomes a command, pathname argument, script or child-process environment.
	command := exec.CommandContext(ctx, executable, "host-folder-picker")
	configurePickerProcess(command)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "RESONANCE_DATABASE_URL") && !strings.EqualFold(key, "PGPASSWORD") {
			command.Env = append(command.Env, entry)
		}
	}
	var output pickerOutput
	reader, hold, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	defer hold.Close()
	// The child owns only the read end. If the server dies, Windows closes the
	// parent's write end, and the helper exits rather than orphaning its dialog.
	command.Stdin = reader
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, err
	}
	reader.Close()
	if err := command.Wait(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func runHostFolderPicker(output io.Writer) error {
	watchPickerParent(os.Stdin, func() { os.Exit(1) })
	choice, err := nativeChooseFolder()
	if err != nil {
		return errPickerUnsupported
	}
	if !choice.Cancelled && !validAdminPath(choice.Path) {
		return errors.New("selected folder is not a supported local path")
	}
	return json.NewEncoder(output).Encode(choice)
}
func watchPickerParent(input io.Reader, exit func()) {
	go func() { _, _ = io.Copy(io.Discard, input); exit() }()
}
