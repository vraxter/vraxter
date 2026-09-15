// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package main

import (
	"fmt"
	"github.com/patagonicrune/vraxter/internal/config"
	"github.com/patagonicrune/vraxter/internal/db"
)

func main() {
	cfg := config.Load()
	store, _ := db.NewStore(cfg.DBPath)
	defer store.Close()

	_, err := store.Conn.Exec("UPDATE models SET priority = 10 WHERE id = 'default-ollama'")
	if err != nil {
		fmt.Println("Error updating:", err)
		return
	}
	fmt.Println("Prioridad de Ollama bajada a 10 con exito.")
}
