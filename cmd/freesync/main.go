// Free Sync — CLI entrypoint (see SPEC.md).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		fmt.Fprintln(os.Stderr, "usage: freesync run")
		os.Exit(2)
	}
	fmt.Println("freesync: run not yet implemented (TDD in progress)")
}
