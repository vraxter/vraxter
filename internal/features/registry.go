// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package features

import "fmt"

// Feature defines a module that can be conditionally activated based on the active Implementation World.
type Feature interface {
	// ID returns a unique identifier for the feature.
	ID() string
	// Implementations returns the list of worlds where this feature should be active.
	Implementations() []string
	// Activate runs the initialization logic for the feature.
	Activate() error
}

var registry []Feature

// Register adds a feature to the global registry. Usually called in init().
func Register(f Feature) {
	registry = append(registry, f)
}

// ActivateForWorld iterates through registered features and activates those that belong to the active world.
func ActivateForWorld(world string) {
	fmt.Printf("🌍 Initializing Implementation World: [%s]\n", world)
	for _, f := range registry {
		if world == "sovereign" || contains(f.Implementations(), world) {
			if err := f.Activate(); err != nil {
				fmt.Printf("⚠️ Failed to activate feature '%s': %v\n", f.ID(), err)
			} else {
				fmt.Printf("✅ Activated Power: %s\n", f.ID())
			}
		}
	}
}

func contains(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
