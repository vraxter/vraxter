// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"context"
	"fmt"
	"os"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/spf13/cobra"
)

var tokenScopes string

var tokensCmd = &cobra.Command{
	Use:   "tokens",
	Short: "Manage scoped API tokens for external clients",
}

var createTokenCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Generate a new scoped token",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Printf("❌ Failed to connect: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.GenerateToken(context.Background(), &v1.GenerateTokenRequest{
			Name:   args[0],
			Scopes: tokenScopes,
		})
		if err != nil {
			fmt.Printf("❌ Failed to create token: %v\n", err)
			return
		}

		fmt.Printf("✅ Token successfully created! (ID: %s)\n", res.Id)
		fmt.Println("⚠️  WARNING: This is the ONLY time you will see this token. Save it securely.")
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Println(res.Token)
		fmt.Println("--------------------------------------------------------------------------------")
	},
}

var listTokensCmd = &cobra.Command{
	Use:   "ls",
	Short: "List active API tokens",
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Printf("❌ Failed to connect: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.ListTokens(context.Background(), &v1.ListTokensRequest{})
		if err != nil {
			fmt.Printf("❌ Failed to list tokens: %v\n", err)
			return
		}

		if len(res.Tokens) == 0 {
			fmt.Println("📭 No active tokens found.")
			return
		}

		fmt.Println("\n◈ VRAXTER API TOKENS")
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Printf("%-36s %-20s %-20s\n", "ID", "NAME", "SCOPES")
		fmt.Println("--------------------------------------------------------------------------------")
		for _, t := range res.Tokens {
			fmt.Printf("%-36s %-20s %-20s\n", t.Id, t.Name, t.Scopes)
		}
		fmt.Println("--------------------------------------------------------------------------------")
	},
}

var revokeTokenCmd = &cobra.Command{
	Use:   "revoke <id>",
	Short: "Revoke an API token",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Printf("❌ Failed to connect: %v\n", err)
			os.Exit(1)
		}
		defer gClient.Close()

		res, err := gClient.RevokeToken(context.Background(), &v1.RevokeTokenRequest{Id: args[0]})
		if err != nil {
			fmt.Printf("❌ Revocation failed: %v\n", err)
			return
		}

		if res.Success {
			fmt.Printf("🗑️ %s\n", res.Message)
		} else {
			fmt.Printf("🚫 Failed: %s\n", res.Message)
		}
	},
}

func init() {
	createTokenCmd.Flags().StringVarP(&tokenScopes, "scopes", "s", "chat:write", "Comma separated scopes (e.g., chat:write,skills:read)")
	
	tokensCmd.AddCommand(createTokenCmd)
	tokensCmd.AddCommand(listTokensCmd)
	tokensCmd.AddCommand(revokeTokenCmd)
	
	rootCmd.AddCommand(tokensCmd)
}
