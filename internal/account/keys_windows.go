//go:build windows

package account

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// keyEntropy is mixed into the DPAPI encryption so a blob copied to another account's file does not decrypt.
func keyEntropy(id string) []byte { return []byte("seelegent-api-key:" + id) }

// protect encrypts data with DPAPI for the current Windows user. No UI is ever shown.
func protect(data []byte, id string) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("protect: empty data")
	}
	entropy := keyEntropy(id)
	in := blobOf(data)
	ent := blobOf(entropy)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	return takeBlob(&out), nil
}

// unprotect decrypts a blob made by protect.
func unprotect(blob []byte, id string) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("unprotect: empty file")
	}
	entropy := keyEntropy(id)
	in := blobOf(blob)
	ent := blobOf(entropy)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &ent, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	return takeBlob(&out), nil
}

// blobOf points a DataBlob at b (which must not be empty).
func blobOf(b []byte) windows.DataBlob {
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]} //nolint:gosec // lengths are capped far below 4 GiB
}

// takeBlob copies the system-allocated output blob into Go memory, wipes it and frees it.
func takeBlob(b *windows.DataBlob) []byte {
	if b.Data == nil {
		return nil
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(b.Data))) }()
	src := unsafe.Slice(b.Data, b.Size)
	out := make([]byte, len(src))
	copy(out, src)
	clear(src)
	return out
}
