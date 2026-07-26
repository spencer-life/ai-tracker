package ingest

import (
	"os"
	"reflect"
)

// fileIdentity extracts Unix-style device and inode fields without importing
// syscall.Stat_t, which is not available when cross-compiling for Windows.
// Filesystems without those fields safely fall back to size and mtime checks.
func fileIdentity(info os.FileInfo) (device, inode uint64) {
	value := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0, 0
	}
	if field := value.FieldByName("Dev"); field.IsValid() && field.CanUint() {
		device = field.Uint()
	}
	if field := value.FieldByName("Ino"); field.IsValid() && field.CanUint() {
		inode = field.Uint()
	}
	return device, inode
}
