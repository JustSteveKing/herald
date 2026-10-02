// Command herald sends fake, correctly signed provider webhooks to any URL.
package main

import (
	"fmt"
	"os"

	// Before anything imports Bubble Tea: see the package doc.
	_ "github.com/JustSteveKing/herald/internal/termquiet"
)

func main() {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "herald:", err)
		os.Exit(1)
	}
}
