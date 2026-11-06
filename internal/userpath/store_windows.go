//go:build windows

package userpath

import (
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const environmentKey = `Environment`

type windowsStore struct{}

// NewStore returns the HKCU-backed user PATH store.
func NewStore() Store { return windowsStore{} }

func (windowsStore) Read() (Value, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, environmentKey, registry.QUERY_VALUE)
	if err != nil {
		if err == registry.ErrNotExist {
			return Value{Expand: true}, nil
		}
		return Value{}, err
	}
	defer k.Close()
	raw, typ, err := k.GetStringValue("Path")
	if err != nil {
		if err == registry.ErrNotExist {
			return Value{Expand: true}, nil
		}
		return Value{}, err
	}
	return Value{Raw: raw, Expand: typ == registry.EXPAND_SZ}, nil
}

func (windowsStore) Write(v Value) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, environmentKey,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		if err != registry.ErrNotExist {
			return err
		}
		k, _, err = registry.CreateKey(registry.CURRENT_USER, environmentKey,
			registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			return err
		}
	}
	defer k.Close()
	if v.Expand {
		err = k.SetExpandStringValue("Path", v.Raw)
	} else {
		err = k.SetStringValue("Path", v.Raw)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procSendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")
)

// broadcastEnvironmentChange asks Explorer to refresh its environment block so
// terminals started from the Start menu pick up the new PATH without logoff.
func broadcastEnvironmentChange() {
	name, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
		timeoutMillis   = 5000
	)
	var result uintptr
	_, _, _ = procSendMessageTimeoutW.Call(
		hwndBroadcast,
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(name)),
		smtoAbortIfHung,
		timeoutMillis,
		uintptr(unsafe.Pointer(&result)),
	)
}
