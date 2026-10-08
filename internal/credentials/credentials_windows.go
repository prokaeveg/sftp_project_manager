//go:build windows

package credentials

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	credReadW      = advapi32.NewProc("CredReadW")
	credWriteW     = advapi32.NewProc("CredWriteW")
	credDeleteW    = advapi32.NewProc("CredDeleteW")
	credEnumerateW = advapi32.NewProc("CredEnumerateW")
	credFree       = advapi32.NewProc("CredFree")
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [2]uint32
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsStore struct{}

// New returns a store backed by the current user's Windows Credential Manager.
func New() Store { return windowsStore{} }

func (windowsStore) Read(target string) (string, bool, error) {
	name, err := qualifiedTarget(target)
	if err != nil {
		return "", false, err
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", false, errors.New("invalid credential target")
	}
	var credentialPtr *windowsCredential
	result, _, callErr := credReadW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&credentialPtr)),
	)
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("CredReadW failed: %w", callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(credentialPtr)))
	if credentialPtr == nil || credentialPtr.CredentialBlobSize > 4096 {
		return "", false, errors.New("credential data is invalid")
	}
	if credentialPtr.CredentialBlobSize == 0 {
		return "", true, nil
	}
	if credentialPtr.CredentialBlob == nil {
		return "", false, errors.New("credential data is invalid")
	}
	data := unsafe.Slice(credentialPtr.CredentialBlob, credentialPtr.CredentialBlobSize)
	value := string(data)
	if err := validateValue(value); err != nil {
		return "", false, errors.New("credential data is invalid")
	}
	return value, true, nil
}

func (windowsStore) Write(target, value string) error {
	name, err := qualifiedTarget(target)
	if err != nil {
		return err
	}
	if err := validateValue(value); err != nil {
		return err
	}
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return errors.New("invalid credential target")
	}
	data := []byte(value)
	credential := windowsCredential{
		Type:               credTypeGeneric,
		TargetName:         namePtr,
		CredentialBlobSize: uint32(len(data)),
		Persist:            credPersistLocalMachine,
	}
	if len(data) > 0 {
		credential.CredentialBlob = &data[0]
	}
	result, _, callErr := credWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	runtime.KeepAlive(namePtr)
	runtime.KeepAlive(data)
	if result == 0 {
		return fmt.Errorf("CredWriteW failed: %w", callErr)
	}
	return nil
}

func (windowsStore) DeletePrefix(prefix string) error {
	qualified, err := qualifiedPrefix(prefix)
	if err != nil {
		return err
	}
	filter, err := windows.UTF16PtrFromString(namespace + "*")
	if err != nil {
		return errors.New("invalid credential namespace")
	}
	var count uint32
	var entries **windowsCredential
	result, _, callErr := credEnumerateW.Call(
		uintptr(unsafe.Pointer(filter)),
		0,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&entries)),
	)
	runtime.KeepAlive(filter)
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil
		}
		return fmt.Errorf("CredEnumerateW failed: %w", callErr)
	}
	if entries == nil || count == 0 {
		if entries != nil {
			credFree.Call(uintptr(unsafe.Pointer(entries)))
		}
		return nil
	}
	defer credFree.Call(uintptr(unsafe.Pointer(entries)))
	items := unsafe.Slice((**windowsCredential)(unsafe.Pointer(entries)), count)
	for _, item := range items {
		if item == nil || item.TargetName == nil {
			continue
		}
		name := windows.UTF16PtrToString(item.TargetName)
		// The API filter is a wildcard; enforce the literal app namespace and
		// caller prefix before deleting each enumerated credential.
		if !strings.HasPrefix(name, namespace) || (name != qualified && !strings.HasPrefix(name, qualified+"/")) {
			continue
		}
		namePtr, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return errors.New("invalid credential target returned by Credential Manager")
		}
		deleted, _, deleteErr := credDeleteW.Call(uintptr(unsafe.Pointer(namePtr)), credTypeGeneric, 0)
		runtime.KeepAlive(namePtr)
		if deleted == 0 && !errors.Is(deleteErr, windows.ERROR_NOT_FOUND) {
			return fmt.Errorf("CredDeleteW failed: %w", deleteErr)
		}
	}
	return nil
}
