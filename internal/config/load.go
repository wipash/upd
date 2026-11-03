package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed builtin.toml
var builtinTOML []byte

// BuiltinName is the file name used in errors about the embedded catalog.
const BuiltinName = "builtin.toml"

// Builtin parses the embedded catalog.
func Builtin() (*File, error) {
	return Parse(BuiltinName, builtinTOML)
}

// BuiltinSource returns the embedded catalog as written.
func BuiltinSource() []byte { return builtinTOML }

// UserPath returns the user config file path. It does not check that the
// file exists.
func UserPath() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "upd", "config.toml"), nil
	}
	if runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "upd", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "upd", "config.toml"), nil
}

// Load reads the built-in catalog and the user config at path (which may
// not exist) and merges them.
func Load(path string) (*Config, error) {
	builtin, err := Builtin()
	if err != nil {
		return nil, fmt.Errorf("built-in catalog: %w", err)
	}
	var user *File
	doc, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if user, err = Parse(path, doc); err != nil {
			return nil, err
		}
	}
	return Merge(builtin, user)
}
