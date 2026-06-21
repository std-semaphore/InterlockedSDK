package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const registryURL = "https://ilregistry.wuzzer.uk/api/v1"

var version = "dev"

var rootCmd = &cobra.Command{
	Use:     "intsdk",
	Short:   "Interlocked SDK helps publish and create timetables for the Interlocked Registry",
	Version: version,
}

func main() {
	rootCmd.AddCommand(
		registerCmd(),
		deregisterCmd(),
		whoamiCmd(),
		userCmd(),
		compileCmd(),
		publishCmd(),
		yankCmd(),
		verifyCmd(),
		infoCmd(),
		listCmd(),
		downloadCmd(),
		mapCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
