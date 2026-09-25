package pack_test

import (
	"os"
	"path/filepath"
)

func mkdirAll(root, name string) error {
	return os.MkdirAll(filepath.Join(root, name), 0o755)
}

func writeFile(root, id, name, body string) error {
	return os.WriteFile(filepath.Join(root, id, name), []byte(body), 0o644)
}
