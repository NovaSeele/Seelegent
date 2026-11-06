//go:build !windows

package account

import "errors"

// protect returns data as it is: outside Windows the key file is protected by its 0600 mode under a 0700
// directory.
func protect(data []byte, _ string) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("protect: empty data")
	}
	return data, nil
}

// unprotect returns the stored bytes.
func unprotect(blob []byte, _ string) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("unprotect: empty file")
	}
	return blob, nil
}
