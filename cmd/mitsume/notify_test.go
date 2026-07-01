package main

import (
	"testing"

	"github.com/suecharo/mitsume/internal/config"
)

func TestResolveWebhookEnvName_CLIWins(t *testing.T) {
	cfg := &config.Config{Notify: config.Notify{WebhookURLEnv: "CFG_ENV"}}
	if got := resolveWebhookEnvName("CLI_ENV", cfg); got != "CLI_ENV" {
		t.Fatalf("got %q, want CLI_ENV", got)
	}
}

func TestResolveWebhookEnvName_ConfigWhenNoCLI(t *testing.T) {
	cfg := &config.Config{Notify: config.Notify{WebhookURLEnv: "CFG_ENV"}}
	if got := resolveWebhookEnvName("", cfg); got != "CFG_ENV" {
		t.Fatalf("got %q, want CFG_ENV", got)
	}
}

func TestResolveWebhookEnvName_DefaultWhenAllEmpty(t *testing.T) {
	if got := resolveWebhookEnvName("", nil); got != defaultWebhookEnvKey {
		t.Fatalf("got %q, want %s", got, defaultWebhookEnvKey)
	}
}

func TestResolveWebhookEnvName_DefaultWhenConfigFieldEmpty(t *testing.T) {
	cfg := &config.Config{Notify: config.Notify{}}
	if got := resolveWebhookEnvName("", cfg); got != defaultWebhookEnvKey {
		t.Fatalf("got %q, want %s", got, defaultWebhookEnvKey)
	}
}
