//go:build !windows

package credentials

type unsupportedStore struct{}

// New returns a store that reports unsupported on non-Windows systems.
func New() Store { return unsupportedStore{} }

func (unsupportedStore) Read(string) (string, bool, error) {
	return "", false, ErrUnsupported
}

func (unsupportedStore) Write(string, string) error { return ErrUnsupported }

func (unsupportedStore) DeletePrefix(string) error { return ErrUnsupported }
