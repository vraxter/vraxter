package db

import (
	"context"
	"testing"
)

func TestUserRepo_CreateAndGet(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()
	repo := NewUserRepository(store)
	ctx := context.Background()

	profile := UserProfile{
		ID:        "user-1",
		Name:      "Admin",
		Expertise: "root",
	}

	err := repo.CreateUser(ctx, &profile)
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}

	u, err := repo.GetDefaultUser(ctx)
	if err != nil {
		t.Fatalf("Failed to get default user: %v", err)
	}
	if u.Name != "Admin" {
		t.Errorf("Expected user 'Admin', got '%s'", u.Name)
	}

	u2, err := repo.GetUserByName(ctx, "Admin")
	if err != nil {
		t.Fatalf("Failed to get user by name: %v", err)
	}
	if u2.ID != "user-1" {
		t.Errorf("Expected ID 'user-1', got '%s'", u2.ID)
	}

	profile.Expertise = "user"
	err = repo.UpdateUser(ctx, &profile)
	if err != nil {
		t.Fatalf("Failed to update user: %v", err)
	}

	u3, _ := repo.GetUserByName(ctx, "Admin")
	if u3.Expertise != "user" {
		t.Errorf("Expected expertise 'user', got '%s'", u3.Expertise)
	}
}
