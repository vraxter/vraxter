package tests

import (
	"fmt"
	"os"
	"path/filepath"

	"testing"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
)

func TestCoders(t *testing.T) {
	t.Log("🧪 Iniciando Test de Arquitectura Polimórfica...")

	// 1. Setup Temp Environment
	tmpDir, _ := os.MkdirTemp("", "vraxter-test-*")
	dbPath := filepath.Join(tmpDir, "test.db")
	sdkDir := filepath.Join(tmpDir, "sdk")
	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	fmt.Printf("📂 Entorno temporal: %s\n", tmpDir)

	store, err := db.NewStore(dbPath)
	if err != nil {
		t.Fatalf("❌ Error creating test store: %v", err)
	}
	repo := db.NewSkillRepository(store)
	tm := services.NewToolchainManager(sdkDir)
	runner := skills.NewRunner()
	coder := services.NewCoderService(repo, tm, skillsDir, runner)

	// 2. Test GO Compilation
	goCode := `package main
import ("encoding/json"; "os")
type Res struct { JSONRPC string "json:\"jsonrpc\""; ID string "json:\"id\""; Result map[string]interface{} "json:\"result\"" }
func main() {
    res := Res{JSONRPC: "2.0", ID: "1", Result: map[string]interface{}{"status": "completed", "output": "GO_SUCCESS"}}
    json.NewEncoder(os.Stdout).Encode(res)
}`

	fmt.Println("\n🛠️  Probando GoCoder...")
	err = coder.CreateSkill("go", "TestGo", "A test go skill", "", goCode, nil)
	if err != nil {
		fmt.Printf("❌ Error en GoCoder: %v\n", err)
	} else {
		fmt.Println("✅ GoCoder: Habilidad compilada y registrada.")
	}

	// 3. Test RUST Compilation
	// Note: This will download 200MB if rustc is not in PATH.
	// To avoid timeout in test, we only try if rustc exists or we skip.
	rustcPath := tm.GetRustcPath()
	if rustcPath != "" {
		fmt.Println("\n🛠️  Probando RustCoder...")
		rustCode := `fn main() {
    println!("{{\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{{\"status\":\"completed\",\"output\":\"RUST_SUCCESS\"}}}}");
}`
		err = coder.CreateSkill("rust", "TestRust", "A test rust skill", "", rustCode, nil)
		if err != nil {
			fmt.Printf("❌ Error en RustCoder: %v\n", err)
		} else {
			fmt.Println("✅ RustCoder: Habilidad compilada y registrada.")
		}
	} else {
		fmt.Println("\n⏭️  Saltando Test de Rust (Requiere descarga de SDK).")
	}

	// 4. Verify DB — Vraxter Shield Summary
	skills, _ := repo.GetAllSkills()
	fmt.Printf("\n📊 Habilidades en DB: %d\n", len(skills))
	for _, s := range skills {
		official := "Community"
		if s.IsOfficial {
			official = "[OFFICIAL]"
		}
		fmt.Printf("- [%s] %s (%s) | %s | Score: %.1f | Checksum: %s...\n",
			s.Language, s.Name, s.Command, official, s.Score, safePrefix(s.Checksum, 12))
	}

	fmt.Println("\n✨ Test Finalizado.")
}

func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
