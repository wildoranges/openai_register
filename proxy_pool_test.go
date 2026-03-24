package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestProxyPoolStaticRoundRobinAndFailureMarking(t *testing.T) {
	pool := NewProxyPool([]string{"http://proxy-a:8001", "http://proxy-b:8002", "http://proxy-c:8003"})
	for _, p := range pool.proxies {
		p.Available = true
	}

	first := pool.GetNextAssignment()
	if first == nil || first.ProxyURL != "http://proxy-a:8001" {
		t.Fatalf("first assignment mismatch: %+v", first)
	}

	pool.MarkFailedAssignment(first)

	second := pool.GetNextAssignment()
	if second == nil || second.ProxyURL != "http://proxy-b:8002" {
		t.Fatalf("second assignment mismatch: %+v", second)
	}

	third := pool.GetNextAssignment()
	if third == nil || third.ProxyURL != "http://proxy-c:8003" {
		t.Fatalf("third assignment mismatch: %+v", third)
	}

	fourth := pool.GetNextAssignment()
	if fourth == nil || fourth.ProxyURL != "http://proxy-b:8002" {
		t.Fatalf("expected failed proxy to be skipped, got: %+v", fourth)
	}
}

func TestProxyPoolReactivatesAllRuntimeFailedAssignmentsWhenExhausted(t *testing.T) {
	pool := NewProxyPool([]string{"http://proxy-a:8001", "http://proxy-b:8002"})
	for _, p := range pool.proxies {
		p.Available = true
	}

	first := pool.GetNextAssignment()
	if first == nil || first.ProxyURL != "http://proxy-a:8001" {
		t.Fatalf("unexpected first assignment: %+v", first)
	}
	second := pool.GetNextAssignment()
	if second == nil || second.ProxyURL != "http://proxy-b:8002" {
		t.Fatalf("unexpected second assignment: %+v", second)
	}

	pool.MarkFailedAssignment(first)
	pool.MarkFailedAssignment(second)

	reactivated := pool.GetNextAssignment()
	if reactivated == nil {
		t.Fatalf("expected recycled assignment after full runtime exhaustion")
	}
	if reactivated.ProxyURL != "http://proxy-a:8001" {
		t.Fatalf("expected round-robin to resume with proxy-a, got %+v", reactivated)
	}
	if !pool.proxies[0].Available || !pool.proxies[1].Available {
		t.Fatalf("expected all runtime-failed proxies to be reactivated")
	}
	if pool.proxies[0].RuntimeDisabled || pool.proxies[1].RuntimeDisabled {
		t.Fatalf("expected runtime-disabled markers to be cleared after reactivation")
	}
}

func TestProxyPoolGetNextAssignmentKeepsStartupFailedProxiesDisabled(t *testing.T) {
	pool := NewProxyPool([]string{"http://proxy-a:8001", "http://proxy-b:8002"})
	pool.proxies[0].Available = false
	pool.proxies[0].Error = "启动探活失败"
	pool.proxies[1].Available = true

	assignment := pool.GetNextAssignment()
	if assignment == nil || assignment.ProxyURL != "http://proxy-b:8002" {
		t.Fatalf("expected available proxy-b assignment, got %+v", assignment)
	}

	pool.MarkFailedAssignment(assignment)

	recycled := pool.GetNextAssignment()
	if recycled == nil {
		t.Fatalf("expected runtime-failed proxy-b to be reactivated")
	}
	if recycled.ProxyURL != "http://proxy-b:8002" {
		t.Fatalf("expected only proxy-b to be recyclable, got %+v", recycled)
	}
	if pool.proxies[0].Available {
		t.Fatalf("expected startup-failed proxy-a to remain unavailable")
	}
	if pool.proxies[0].RuntimeDisabled {
		t.Fatalf("did not expect startup-failed proxy-a to be marked runtime-disabled")
	}
}

func TestProxyPoolMarkFailedAssignmentOnlyAffectsSelectedClashNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/proxies/PROXY" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	clash, err := newClashClient(server.URL, "")
	if err != nil {
		t.Fatalf("newClashClient failed: %v", err)
	}

	pool := &ProxyPool{
		proxies: []*ProxyStatus{
			{ID: "clash:node-a", Source: ProxySourceClash, URL: "http://127.0.0.1:7890", Node: "Node-A", Group: "PROXY", Available: true},
			{ID: "clash:node-b", Source: ProxySourceClash, URL: "http://127.0.0.1:7890", Node: "Node-B", Group: "PROXY", Available: true},
		},
		clash: clash,
	}

	first := pool.GetNextAssignment()
	if first == nil || first.ID != "clash:node-a" {
		t.Fatalf("unexpected first assignment: %+v", first)
	}

	pool.MarkFailedAssignment(first)

	next := pool.GetNextAssignment()
	if next == nil {
		t.Fatalf("expected next assignment, got nil")
	}
	if next.ID != "clash:node-b" {
		t.Fatalf("expected second clash node after failure, got %+v", next)
	}

	if pool.proxies[0].Available {
		t.Fatalf("expected failed node-a to be unavailable")
	}
	if !pool.proxies[1].Available {
		t.Fatalf("expected node-b to remain available")
	}
}

func TestNewClashProxyPoolDiscoverySelectionAndFiltering(t *testing.T) {
	var mu sync.Mutex
	putCount := 0
	putAuth := ""
	putBodyName := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"proxies":{"PROXY":{"type":"Selector","all":["Node-US-1","Node-HK-1","DIRECT"]}}}`)
		case r.Method == http.MethodPut && r.URL.Path == "/proxies/PROXY":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode put payload failed: %v", err)
			}
			mu.Lock()
			putCount++
			putAuth = r.Header.Get("Authorization")
			putBodyName = payload["name"]
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	pool, err := NewClashProxyPool(&ClashConfig{
		ExternalController: server.URL,
		Secret:             "top-secret",
		ProxyGroup:         "PROXY",
		MixedProxy:         "127.0.0.1:7890",
		Include:            []string{"US", "HK"},
		Exclude:            []string{"HK"},
	})
	if err != nil {
		t.Fatalf("NewClashProxyPool failed: %v", err)
	}

	if len(pool.proxies) != 1 {
		t.Fatalf("expected only one filtered node, got %d", len(pool.proxies))
	}

	pool.proxies[0].Available = true
	assignment := pool.GetNextAssignment()
	if assignment == nil {
		t.Fatalf("expected assignment, got nil")
	}
	if assignment.Source != ProxySourceClash {
		t.Fatalf("expected clash source, got %s", assignment.Source)
	}
	if assignment.Node != "Node-US-1" {
		t.Fatalf("expected selected node Node-US-1, got %s", assignment.Node)
	}
	if assignment.Group != "PROXY" {
		t.Fatalf("expected group PROXY, got %s", assignment.Group)
	}
	if assignment.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("expected mixed proxy transport URL, got %s", assignment.ProxyURL)
	}

	mu.Lock()
	defer mu.Unlock()
	if putCount == 0 {
		t.Fatalf("expected clash node selection PUT request")
	}
	if putAuth != "Bearer top-secret" {
		t.Fatalf("expected bearer auth header, got %q", putAuth)
	}
	if putBodyName != "Node-US-1" {
		t.Fatalf("expected selected node payload Node-US-1, got %q", putBodyName)
	}
}

func TestNewClashProxyPoolReturnsErrorOnEmptyFilteredNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/proxies" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"proxies":{"PROXY":{"type":"Selector","all":["JP-1","JP-2"]}}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	_, err := NewClashProxyPool(&ClashConfig{
		ExternalController: server.URL,
		ProxyGroup:         "PROXY",
		MixedProxy:         "127.0.0.1:7890",
		Include:            []string{"US"},
	})
	if err == nil {
		t.Fatalf("expected error when no nodes remain after filtering")
	}
	if !strings.Contains(err.Error(), "过滤后无节点") {
		t.Fatalf("unexpected error: %v", err)
	}
}
