package types

import "time"

type UserProfile struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Language        string    `json:"language"`
	ThemePreference string    `json:"theme_preference"`
	RegisteredAt    time.Time `json:"registered_at"`
}
