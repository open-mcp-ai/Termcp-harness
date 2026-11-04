package tokenfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const maxBytes = 4096

// Read loads one control token without placing it in arguments or logs.
func Read(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open token file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat token file: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("token file must not be accessible by group or others")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	if len(data) > maxBytes {
		return "", fmt.Errorf("token file exceeds %d bytes", maxBytes)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("token file is empty")
	}
	return token, nil
}
