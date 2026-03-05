package main

import (
	"strings"
	"testing"
)

func TestGeneratePasswordLengthAndCharset(t *testing.T) {
	allowed := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	for i := 0; i < 50; i++ {
		p := GeneratePassword()
		if len(p) < 16 || len(p) > 23 {
			t.Fatalf("unexpected length %d for password %q", len(p), p)
		}
		for _, ch := range p {
			if !strings.ContainsRune(allowed, ch) {
				t.Fatalf("invalid char %q in password %q", ch, p)
			}
		}
	}
}

func TestRandomStringLengthAndCharset(t *testing.T) {
	allowed := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	s := randomString(32)
	if len(s) != 32 {
		t.Fatalf("expected len=32, got %d", len(s))
	}
	for _, ch := range s {
		if !strings.ContainsRune(allowed, ch) {
			t.Fatalf("invalid char %q in random string %q", ch, s)
		}
	}
}

func TestMin(t *testing.T) {
	if min(1, 2) != 1 {
		t.Fatalf("min(1,2) should be 1")
	}
	if min(3, -5) != -5 {
		t.Fatalf("min(3,-5) should be -5")
	}
	if min(7, 7) != 7 {
		t.Fatalf("min(7,7) should be 7")
	}
}
