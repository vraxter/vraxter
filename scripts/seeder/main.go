package main

import (
	"fmt"
	"os"

	"github.com/patagonicrune/vraxter/internal/config"
	"github.com/patagonicrune/vraxter/internal/db"
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
