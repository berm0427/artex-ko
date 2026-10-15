//go:build !windows

package siwc

func protect(b []byte) ([]byte, error)   { return b, nil }
func unprotect(b []byte) ([]byte, error) { return b, nil }
