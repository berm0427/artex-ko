//go:build windows

package siwc

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func blob(b []byte) windows.DataBlob {
	if len(b) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
}

func crypt(b []byte, encrypt bool) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty credential record")
	}
	in := blob(b)
	var out windows.DataBlob
	var err error
	if encrypt {
		err = windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	} else {
		err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	}
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func protect(b []byte) ([]byte, error)   { return crypt(b, true) }
func unprotect(b []byte) ([]byte, error) { return crypt(b, false) }
