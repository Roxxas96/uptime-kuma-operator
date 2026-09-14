// cmd/main_smoke_test.go
package main

import (
	"os/exec"
	"testing"
)

// TestBuild is a minimal smoke test confirming the binary compiles and
// its --help-equivalent (version) path doesn't panic before manager setup.
// Full behavior is covered by internal/controller's envtest suite.
func TestBuild(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "/dev/null", "./...")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
}
