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
	"os"

	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/db"
)

func main() {
	cfg := config.Load()
	store, _ := db.NewStore(cfg.DBPath)
	defer store.Close()

	cwd, _ := os.Getwd()
	wasmPath := cwd + "/examples/hello-wasm/hello.wasm"

	query := `INSERT OR REPLACE INTO skills (id, name, description, version, command, language, engine, tier, score, is_official, checksum)
	VALUES ('hello-world-greet', 'Greeter Tool', 'Skill que sirve especificamente para saludar al humano. Usala cuando el usuario pida dar un saludo especial web-assembly', '1.0', ?, 'go', 'wasm', 1, 5.0, 1, 'mock-checksum');`

	_, err := store.Conn.Exec(query, wasmPath)
	if err != nil {
		fmt.Println("Error inserting skill:", err)
		return
	}
	fmt.Println("WASM Skill insertada en BD exitosamente.")
}
