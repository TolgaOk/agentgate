package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/TolgaOk/agentgate/internal/auth"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage provider authentication",
	}

	openaiCmd := &cobra.Command{
		Use:   "openai",
		Short: "Authenticate with OpenAI via OAuth (Sign in with ChatGPT)",
		RunE:  runAuthOpenAI,
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for all providers",
		RunE:  runAuthStatus,
	}

	cmd.AddCommand(openaiCmd, statusCmd)
	return cmd
}

func runAuthOpenAI(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg := auth.OpenAIOAuth()
	tok, err := auth.RunOAuthFlow(ctx, cfg)
	if err != nil {
		return fmt.Errorf("auth openai: %w", err)
	}

	store, err := auth.LoadStore()
	if err != nil {
		return fmt.Errorf("auth openai: %w", err)
	}
	if err := store.Set("openai", tok); err != nil {
		return fmt.Errorf("auth openai: %w", err)
	}

	fmt.Println("OpenAI authentication successful!")
	fmt.Printf("Token expires: %s\n", tok.ExpiresAt.Format(time.RFC3339))
	return nil
}

func runAuthStatus(cmd *cobra.Command, args []string) error {
	store, err := auth.LoadStore()
	if err != nil {
		return fmt.Errorf("auth status: %w", err)
	}

	providers := store.Providers()
	if len(providers) == 0 {
		fmt.Println("No OAuth tokens stored.")
		fmt.Println("Run 'ag auth openai' to authenticate with OpenAI.")
		return nil
	}

	for _, name := range providers {
		tok := store.Get(name)
		status := "valid"
		if tok.Expired() {
			if tok.RefreshToken != "" {
				status = "expired (has refresh token)"
			} else {
				status = "expired"
			}
		}
		fmt.Printf("%-12s %s  (expires %s)\n", name, status, tok.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}
