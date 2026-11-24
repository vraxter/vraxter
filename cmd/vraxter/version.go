// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Version values populated by go build -ldflags
var (
	Version   = "1.0.0"
	Edition   = "community"
	Commit    = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print Vraxter runtime version and build metadata",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Vraxter Sovereign Autonomous Execution Runtime\n")
		fmt.Printf("Version:    %s\n", Version)
		fmt.Printf("Edition:    %s\n", Edition)
		fmt.Printf("Commit:     %s\n", Commit)
		fmt.Printf("Built:      %s\n", BuildDate)
		fmt.Printf("Go version: %s\n", runtime.Version())
		fmt.Printf("OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
