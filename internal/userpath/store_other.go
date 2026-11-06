//go:build !windows

package userpath

import "errors"

type unsupportedStore struct{}

// NewStore returns a no-op store: shell rc files are never edited.
func NewStore() Store { return unsupportedStore{} }

func (unsupportedStore) Read() (Value, error) {
	return Value{}, errors.New("user PATH is only managed on Windows")
}

func (unsupportedStore) Write(Value) error {
	return errors.New("user PATH is only managed on Windows")
}
