//go:build tools

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tc-hib/winres"
)

func main() {
	iconPath := flag.String("icon", "desktop/build/windows/icon.ico", "source Windows icon")
	outputDir := flag.String("out", "cmd/termcp-harness", "Go package directory receiving architecture-specific resources")
	flag.Parse()

	iconFile, err := os.Open(*iconPath)
	if err != nil {
		fail("open icon: %v", err)
	}
	defer iconFile.Close()
	icon, err := winres.LoadICO(iconFile)
	if err != nil {
		fail("load icon: %v", err)
	}
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		fail("create output directory: %v", err)
	}

	targets := []struct {
		name string
		arch winres.Arch
	}{
		{name: "termcp_harness_windows_amd64.syso", arch: winres.ArchAMD64},
		{name: "termcp_harness_windows_arm64.syso", arch: winres.ArchARM64},
	}
	for _, target := range targets {
		resources := winres.ResourceSet{}
		if err := resources.SetIcon(winres.ID(1), icon); err != nil {
			fail("set icon for %s: %v", target.name, err)
		}
		path := filepath.Join(*outputDir, target.name)
		output, err := os.Create(path)
		if err != nil {
			fail("create %s: %v", path, err)
		}
		writeErr := resources.WriteObject(output, target.arch)
		closeErr := output.Close()
		if writeErr != nil {
			fail("write %s: %v", path, writeErr)
		}
		if closeErr != nil {
			fail("close %s: %v", path, closeErr)
		}
		fmt.Println(path)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
