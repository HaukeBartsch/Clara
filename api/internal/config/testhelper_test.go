package config

import "os"

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func removeFile(path string) error { return os.Remove(path) }
