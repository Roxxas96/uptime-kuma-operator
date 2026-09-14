package integration

import "os"

func getenv(key string) string { return os.Getenv(key) }
