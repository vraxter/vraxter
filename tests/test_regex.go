// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"fmt"
	"os"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/pkg/types"
)

func main() {
	home, _ := os.UserHomeDir()
	store, err := db.NewStore(home + "/.vraxter/vraxter.db")
	if err != nil {
		panic(err)
	}

	repo := db.NewSkillRepository(store)

	skill := types.SkillManifest{
		ID:          "vraxter-currency",
		Name:        "Currency Converter",
		Description: "Convert currencies",
		Version:     "1.0",
		Command:     "echo 'Converted!'", // Dummy
		Language:    "bash",
		Engine:      "native",
		ParamRegex:  `(?i)(?P<amount>\d+(?:\.\d+)?)\s+(?P<source>[a-zA-Z]{3})\s+(?:to|in)\s+(?P<target>[a-zA-Z]{3})`,
	}

	err = repo.UpsertSkill(skill)
	if err != nil {
		panic(err)
	}
	fmt.Println("Skill inserted with ParamRegex:", skill.ParamRegex)
}
