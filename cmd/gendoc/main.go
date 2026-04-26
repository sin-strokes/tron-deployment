// Command gendoc emits man pages and markdown reference docs for trond.
//
//	go run ./cmd/gendoc man   ./dist/man     # man(1) output
//	go run ./cmd/gendoc md    ./dist/docs    # markdown per-command
//
// Goreleaser invokes this as a build hook so each release ships a
// pre-rendered `man` directory; `homebrew install` then drops the pages
// into the right place under prefix/share/man/.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra/doc"

	cmd "github.com/tronprotocol/tron-deployment/cmd"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gendoc <man|md> <output-dir>")
		os.Exit(2)
	}
	mode, dir := os.Args[1], os.Args[2]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	root := cmd.Root()
	switch mode {
	case "man":
		hdr := &doc.GenManHeader{
			Title:   "TROND",
			Section: "1",
			Source:  "trond",
			Manual:  "TRON Deployment CLI",
		}
		if err := doc.GenManTree(root, hdr, dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "md":
		if err := doc.GenMarkdownTree(root, dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "mode must be man|md")
		os.Exit(2)
	}

	fmt.Printf("trond %s docs written to %s\n", mode, filepath.Clean(dir))
}
