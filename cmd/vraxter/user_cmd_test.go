// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import "testing"

func TestUserCommandRegisteredOnRoot(t *testing.T) {
	foundUser := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "user" {
			foundUser = true
			sub := make(map[string]bool)
			for _, sc := range c.Commands() {
				sub[sc.Name()] = true
			}
			for _, want := range []string{"profile", "update", "setup"} {
				if !sub[want] {
					t.Errorf("expected 'user %s' subcommand to be registered", want)
				}
			}
		}
	}
	if !foundUser {
		t.Fatal("expected 'user' command to be registered on rootCmd")
	}
}
