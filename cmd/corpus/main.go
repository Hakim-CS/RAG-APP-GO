// Command corpus regenerates corpus/ from the installed Go toolchain's
// standard-library documentation (`go doc -all <pkg>`). The output is
// committed so the eval set and its baseline stay pinned to one corpus;
// rerun this only when deliberately moving to a newer Go release, then
// re-run the eval and update eval/baseline.json.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var packages = []string{
	"bufio", "bytes", "context", "database/sql", "embed", "encoding/json",
	"errors", "flag", "fmt", "io", "log/slog", "maps", "net/http", "os",
	"os/exec", "path/filepath", "regexp", "slices", "sort", "strconv",
	"strings", "sync", "sync/atomic", "testing", "time",
}

func main() {
	dir := "corpus"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, pkg := range packages {
		out, err := exec.Command("go", "doc", "-all", pkg).Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "go doc %s: %v\n", pkg, err)
			os.Exit(1)
		}
		name := strings.ReplaceAll(pkg, "/", "_") + ".txt"
		if err := os.WriteFile(filepath.Join(dir, name), out, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%-16s %7d bytes\n", name, len(out))
	}
}
