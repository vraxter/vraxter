// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for the MVP local UI
		return true
	},
}

// SystemState represents what Vraxter is doing right now (used by "The Eye")
type SystemState string

const (
	StateIdle       SystemState = "idle"
	StateListening  SystemState = "listening"
	StateThinking   SystemState = "thinking"
	StateExecuting  SystemState = "executing"
	StateError      SystemState = "error"
	StateUnverified SystemState = "unverified"
)

// UIEvent is pushed from the backend to the frontend UI
type UIEvent struct {
	Type      string      `json:"type"` // e.g., "state_change", "intent_received"
	State     SystemState `json:"state,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// WSBroker manages all connected clients for real-time events
type WSBroker struct {
	clients map[*websocket.Conn]bool
	mu      sync.Mutex
}

// NewWSBroker creates an instance of the websocket manager
func NewWSBroker() *WSBroker {
	return &WSBroker{
		clients: make(map[*websocket.Conn]bool),
	}
}

// HandleWS upgrades an HTTP connection to a WebSocket
func (b *WSBroker) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade websocket: %v", err)
		return
	}

	b.mu.Lock()
	b.clients[conn] = true
	b.mu.Unlock()

	log.Println("UI Dashboard connected to WebSocket")

	// We only care about writing events to the UI right now, not reading from it via WS
	// However, we need to read to keep the connection alive and detect disconnects
	go func() {
		defer func() {
			b.mu.Lock()
			delete(b.clients, conn)
			b.mu.Unlock()
			conn.Close()
			log.Println("UI Dashboard disconnected")
		}()
		for {
			if _, _, err := conn.NextReader(); err != nil {
				break
			}
		}
	}()
}

// Broadcast sends an event to all connected UI clients (The Eye)
func (b *WSBroker) Broadcast(event UIEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	msg, err := json.Marshal(event)
	if err != nil {
		log.Printf("Failed to encode UI event: %v", err)
		return
	}

	for client := range b.clients {
		if err := client.WriteMessage(websocket.TextMessage, msg); err != nil {
			log.Printf("Error broadcasting to client: %v", err)
			client.Close()
			delete(b.clients, client)
		}
	}
}
