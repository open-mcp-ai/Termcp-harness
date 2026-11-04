package coreinventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientListsBoundedSessions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/sessions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"sessions":[{"id":"core-1","name":"pi","status":"running","ssh_config":"pi","created_at":1}]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Configured || snapshot.Total != 1 || snapshot.Sessions[0].ID != "core-1" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestClientRejectsDuplicateSessionIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sessions":[{"id":"same","status":"running"},{"id":"same","status":"exited"}]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.List(context.Background()); err == nil {
		t.Fatal("duplicate IDs were accepted")
	}
}
