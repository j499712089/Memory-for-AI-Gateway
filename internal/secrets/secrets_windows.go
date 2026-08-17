//go:build windows

package secrets

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	crypt32                  = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData     = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData   = crypt32.NewProc("CryptUnprotectData")
)

type WindowsBackend struct{}

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newWindowsBackend() (*WindowsBackend, error) {
	return &WindowsBackend{}, nil
}

// newStubBackend is a stub for non-Windows compatibility
func newStubBackend() (*WindowsBackend, error) {
	return newWindowsBackend()
}

func (b *WindowsBackend) Encrypt(plaintext []byte) ([]byte, error) {
	var inBlob dataBlob
	inBlob.cbData = uint32(len(plaintext))
	inBlob.pbData = &plaintext[0]

	var outBlob dataBlob

	ret, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		0,
		0,
		0,
		0x01, // CRYPTPROTECT_UI_FORBIDDEN
		uintptr(unsafe.Pointer(&outBlob)),
	)

	if ret == 0 {
		return nil, fmt.Errorf("CryptProtectData failed: %w", err)
	}

	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(outBlob.pbData)))

	ciphertext := make([]byte, outBlob.cbData)
	copy(ciphertext, (*[1 << 30]byte)(unsafe.Pointer(outBlob.pbData))[:outBlob.cbData:outBlob.cbData])

	return ciphertext, nil
}

func (b *WindowsBackend) Decrypt(ciphertext []byte) ([]byte, error) {
	var inBlob dataBlob
	inBlob.cbData = uint32(len(ciphertext))
	inBlob.pbData = &ciphertext[0]

	var outBlob dataBlob

	ret, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0,
		0,
		0,
		0,
		0x01, // CRYPTPROTECT_UI_FORBIDDEN
		uintptr(unsafe.Pointer(&outBlob)),
	)

	if ret == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", err)
	}

	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(outBlob.pbData)))

	plaintext := make([]byte, outBlob.cbData)
	copy(plaintext, (*[1 << 30]byte)(unsafe.Pointer(outBlob.pbData))[:outBlob.cbData:outBlob.cbData])

	return plaintext, nil
}
