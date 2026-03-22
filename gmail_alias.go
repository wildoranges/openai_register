package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type GmailAliasGenerator struct {
	credential     *GmailCredential
	usedAliases    map[string]bool
	aliasPasswords map[string]string // alias -> password mapping
	mu             sync.Mutex
	aliasFile      string
	counter        int64 // persistent counter for uniqueness
}

func NewGmailAliasGenerator(cred *GmailCredential) *GmailAliasGenerator {
	gen := &GmailAliasGenerator{
		credential:     cred,
		usedAliases:    make(map[string]bool),
		aliasPasswords: make(map[string]string),
		aliasFile:      getAliasFilePath(cred.Email),
		counter:        time.Now().UnixNano(),
	}
	gen.loadUsedAliases()
	return gen
}

func getAliasFilePath(email string) string {
	homeDir, _ := os.UserHomeDir()
	safeEmail := strings.ReplaceAll(email, "@", "_at_")
	return filepath.Join(homeDir, ".config", "openai-register", fmt.Sprintf("aliases_%s.json", safeEmail))
}

type aliasPersistence struct {
	UsedAliases    map[string]bool   `json:"used_aliases"`
	AliasPasswords map[string]string `json:"alias_passwords"`
	Counter        int64             `json:"counter"`
}

func (g *GmailAliasGenerator) loadUsedAliases() {
	data, err := os.ReadFile(g.aliasFile)
	if err != nil {
		return
	}
	var persist aliasPersistence
	if err := json.Unmarshal(data, &persist); err == nil {
		g.usedAliases = persist.UsedAliases
		g.aliasPasswords = persist.AliasPasswords
		if persist.Counter > 0 {
			g.counter = persist.Counter
		}
	} else {
		json.Unmarshal(data, &g.usedAliases)
	}
}

func (g *GmailAliasGenerator) saveUsedAliases() {
	os.MkdirAll(filepath.Dir(g.aliasFile), 0700)
	persist := aliasPersistence{
		UsedAliases:    g.usedAliases,
		AliasPasswords: g.aliasPasswords,
		Counter:        g.counter,
	}
	data, _ := json.MarshalIndent(persist, "", "  ")
	os.WriteFile(g.aliasFile, data, 0600)
}

// GenerateAlias generates a unique Gmail alias based on the three rules:
// 1. Add dots between characters: zhang.san@gmail.com
// 2. Add +suffix after username: zhangsan+test@gmail.com
// 3. Use googlemail.com domain: zhangsan@googlemail.com
// Combines all three rules for maximum variations
func (g *GmailAliasGenerator) GenerateAlias() string {
	return g.GenerateAliasWithPassword("")
}

// GenerateAliasWithPassword generates a unique alias and stores the password for it
func (g *GmailAliasGenerator) GenerateAliasWithPassword(password string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	baseEmail := g.credential.Email
	atIndex := strings.Index(baseEmail, "@")
	if atIndex == -1 {
		return baseEmail
	}

	username := baseEmail[:atIndex]
	domain := baseEmail[atIndex+1:]
	cleanUsername := strings.ReplaceAll(username, ".", "")

	// Use crypto/rand for better randomness combined with counter and timestamp
	g.counter++
	randBytes := make([]byte, 16)
	rand.Read(randBytes)

	timestamp := time.Now().UnixNano()
	hash := sha256.Sum256(append(
		append(binary.BigEndian.AppendUint64(nil, uint64(timestamp)),
			binary.BigEndian.AppendUint64(nil, uint64(g.counter))...),
		randBytes...,
	))

	for attempt := 0; attempt < 1000; attempt++ {
		alias := g.generateRandomAlias(cleanUsername, domain, hash, attempt)
		if alias != "" && !g.usedAliases[alias] {
			g.usedAliases[alias] = true
			if password != "" {
				g.aliasPasswords[alias] = password
			}
			g.saveUsedAliases()
			return alias
		}
	}

	suffix := fmt.Sprintf("%x", hash[:4])
	alias := fmt.Sprintf("%s+%s@googlemail.com", cleanUsername, suffix)
	if !g.usedAliases[alias] {
		g.usedAliases[alias] = true
		if password != "" {
			g.aliasPasswords[alias] = password
		}
		g.saveUsedAliases()
	}
	return alias
}

func (g *GmailAliasGenerator) generateRandomAlias(username, originalDomain string, hash [32]byte, attempt int) string {
	domain := "googlemail.com"

	rnd := int(hash[attempt%32]) ^ attempt

	dotted := g.addRandomDots(username, hash, attempt)

	suffixFormat := rnd % 4
	var suffix string
	switch suffixFormat {
	case 0:
		suffix = fmt.Sprintf("%x", hash[(attempt+1)%28:(attempt+1)%28+4])
	case 1:
		suffix = fmt.Sprintf("%d", (int(hash[attempt%32])<<8|int(hash[(attempt+1)%32]))%10000)
	case 2:
		prefixes := []string{"a", "b", "c", "d", "e", "f", "x", "y", "z"}
		suffix = prefixes[rnd%len(prefixes)] + fmt.Sprintf("%x", hash[(attempt+2)%30:(attempt+2)%30+3])
	case 3:
		suffix = fmt.Sprintf("%x", hash[(attempt+3)%29:(attempt+3)%29+5])
	}

	return fmt.Sprintf("%s+%s@%s", dotted, suffix, domain)
}

func (g *GmailAliasGenerator) addRandomDots(username string, hash [32]byte, attempt int) string {
	if len(username) < 2 {
		return username
	}

	n := len(username)
	rnd := int(hash[attempt%32]) ^ attempt

	numDots := (rnd % (n - 1)) + 1
	if numDots > 3 {
		numDots = 3
	}
	if numDots > n-1 {
		numDots = n - 1
	}

	positions := make(map[int]bool)
	for i := 0; i < numDots; i++ {
		pos := (rnd + i*7) % (n - 1)
		positions[pos+1] = true
	}

	result := make([]byte, 0, n+len(positions))
	for i, c := range username {
		if positions[i] {
			result = append(result, '.')
		}
		result = append(result, byte(c))
	}

	return string(result)
}

// GetAllUsedAliases returns all used aliases for this Gmail account
func (g *GmailAliasGenerator) GetAllUsedAliases() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	aliases := make([]string, 0, len(g.usedAliases))
	for alias := range g.usedAliases {
		aliases = append(aliases, alias)
	}
	return aliases
}

// GetBaseEmail returns the original Gmail address
func (g *GmailAliasGenerator) GetBaseEmail() string {
	return g.credential.Email
}

// IsAlias checks if an email is an alias of the base email
func (g *GmailAliasGenerator) IsAlias(email string) bool {
	baseEmail := g.credential.Email

	// Extract username from base email
	atIndex := strings.Index(baseEmail, "@")
	if atIndex == -1 {
		return false
	}
	baseUsername := baseEmail[:atIndex]
	cleanBaseUsername := strings.ReplaceAll(baseUsername, ".", "")

	// Extract parts from alias email
	aliasAtIndex := strings.Index(email, "@")
	if aliasAtIndex == -1 {
		return false
	}
	aliasUsername := email[:aliasAtIndex]
	aliasDomain := email[aliasAtIndex+1:]

	// Check domain
	baseDomain := baseEmail[atIndex+1:]
	validDomains := []string{baseDomain, "googlemail.com"}
	validDomain := false
	for _, d := range validDomains {
		if aliasDomain == d {
			validDomain = true
			break
		}
	}
	if !validDomain {
		return false
	}

	// Check username
	// Remove dots and +suffix parts
	aliasUsername = strings.ReplaceAll(aliasUsername, ".", "")
	if plusIndex := strings.Index(aliasUsername, "+"); plusIndex != -1 {
		aliasUsername = aliasUsername[:plusIndex]
	}

	return aliasUsername == cleanBaseUsername
}

// GetPasswordForAlias returns the stored password for an alias, or empty string if not found
func (g *GmailAliasGenerator) GetPasswordForAlias(alias string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.aliasPasswords[alias]
}

// HasAlias checks if an alias has already been used
func (g *GmailAliasGenerator) HasAlias(alias string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.usedAliases[alias]
}
