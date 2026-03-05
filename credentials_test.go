package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveCredentialsWithDirCreatesAllOutputs(t *testing.T) {
	dir := t.TempDir()
	cred := &AccountCredentials{
		Email:       "foo@example.com",
		Password:    "p@ss",
		AccessToken: "token-1",
		UserID:      "user-1",
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}

	if err := SaveCredentialsWithDir(cred, dir); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	jsonPath := filepath.Join(dir, "openai_credentials.json")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("missing %s: %v", jsonPath, err)
	}

	var list []AccountCredentials
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json failed: %v", err)
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(list) != 1 || list[0].Email != cred.Email {
		t.Fatalf("unexpected credentials list: %+v", list)
	}

	authPath := filepath.Join(dir, "auth_foo.json")
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("missing codex auth file: %v", err)
	}

	tokensPath := filepath.Join(dir, "openai_tokens.txt")
	tokens, err := os.ReadFile(tokensPath)
	if err != nil {
		t.Fatalf("read tokens failed: %v", err)
	}
	text := string(tokens)
	if !strings.Contains(text, "OPENAI_EMAIL=foo@example.com") {
		t.Fatalf("token file missing email line: %s", text)
	}
}

func TestSaveCredentialsWithDirUpdatesExistingEmail(t *testing.T) {
	dir := t.TempDir()
	first := &AccountCredentials{
		Email:       "same@example.com",
		Password:    "p1",
		AccessToken: "token-old",
		UserID:      "u1",
		CreatedAt:   time.Now().UTC(),
	}
	second := &AccountCredentials{
		Email:       "same@example.com",
		Password:    "p2",
		AccessToken: "token-new",
		UserID:      "u2",
		CreatedAt:   time.Now().UTC(),
	}

	if err := SaveCredentialsWithDir(first, dir); err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if err := SaveCredentialsWithDir(second, dir); err != nil {
		t.Fatalf("second save failed: %v", err)
	}

	jsonPath := filepath.Join(dir, "openai_credentials.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json failed: %v", err)
	}
	var list []AccountCredentials
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 record after update, got %d", len(list))
	}
	if list[0].AccessToken != "token-new" || list[0].Password != "p2" {
		t.Fatalf("record not updated: %+v", list[0])
	}
}
