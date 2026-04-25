package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	var req struct {
		Params map[string]interface{} `json:"params"`
		ID     string                 `json:"id"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		return
	}

	if req.Params["_vraxter_dry_run"] == true {
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":\"%s\",\"result\":{\"status\":\"completed\",\"output\":\"dry-run success\"}}", req.ID)
		return
	}

	// Logic here
	fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":\"%s\",\"result\":{\"status\":\"completed\",\"output\":\"Success\"}}", req.ID)
}
