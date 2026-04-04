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
