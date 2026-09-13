package main

import (
	"fmt"
	"os"

	"example.com/cobra-tree/cmd"
)

func main() {
	if err := cmd.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
