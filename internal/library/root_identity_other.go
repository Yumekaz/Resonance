//go:build !windows

package library

import (
	"encoding/binary"
	"fmt"
	"os"
	"reflect"
	"runtime"

	"resonance/internal/storage"
)

func platformRootIdentity(file *os.File) (storage.NativeIdentity, bool, error) {
	if file == nil {
		return storage.NativeIdentity{}, false, os.ErrInvalid
	}
	info, err := file.Stat()
	if err != nil {
		return storage.NativeIdentity{}, false, err
	}
	value := reflect.ValueOf(info.Sys())
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return storage.NativeIdentity{}, false, nil
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return storage.NativeIdentity{}, false, nil
	}
	device, okDevice := unsignedStatField(value, "Dev")
	inode, okInode := unsignedStatField(value, "Ino")
	if !okDevice || !okInode {
		return storage.NativeIdentity{}, false, nil
	}
	birth := statBirthToken(value)
	if len(birth) == 0 {
		// A device/inode pair alone can be reused after a disconnected root is
		// replaced. It cannot fence destructive absence across process restarts.
		return storage.NativeIdentity{}, false, nil
	}
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], inode)
	identity := storage.NativeIdentity{
		Kind:       "unix_dev_inode",
		Scope:      fmt.Sprintf("%s-device-%x", runtime.GOOS, device),
		ID:         id[:],
		BirthToken: birth,
	}
	return identity, true, nil
}

func unsignedStatField(value reflect.Value, name string) (uint64, bool) {
	field := value.FieldByName(name)
	if !field.IsValid() {
		return 0, false
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := field.Int()
		return uint64(n), n >= 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint(), true
	default:
		return 0, false
	}
}

func statBirthToken(value reflect.Value) []byte {
	for _, name := range []string{"Birthtimespec", "Btim", "Birthtime"} {
		field := value.FieldByName(name)
		for field.IsValid() && (field.Kind() == reflect.Pointer || field.Kind() == reflect.Interface) {
			if field.IsNil() {
				break
			}
			field = field.Elem()
		}
		if !field.IsValid() || field.Kind() != reflect.Struct {
			continue
		}
		seconds, okSeconds := unsignedStatField(field, "Sec")
		nanos, okNanos := unsignedStatField(field, "Nsec")
		if !okSeconds || !okNanos {
			seconds, okSeconds = unsignedStatField(field, "Tv_sec")
			nanos, okNanos = unsignedStatField(field, "Tv_nsec")
		}
		if okSeconds && okNanos {
			var token [16]byte
			binary.BigEndian.PutUint64(token[:8], seconds)
			binary.BigEndian.PutUint64(token[8:], nanos)
			return token[:]
		}
	}
	return nil
}
