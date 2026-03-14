package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── Store tests ───────────────────────────────────────────────────────────────

func TestStore_SaveAndGet(t *testing.T) {
	s := NewStore()
	n := &Notification{ID: "test-1", Status: StatusQueued}
	s.Save(n)

	got, ok := s.Get("test-1")
	if !ok {
		t.Fatal("expected to find notification")
	}
	if got.ID != "test-1" {
		t.Errorf("expected ID test-1, got %s", got.ID)
	}
}

func TestStore_GetMissing(t *testing.T) {
	s := NewStore()
	_, ok := s.Get("missing")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestStore_UpdateStatus(t *testing.T) {
	s := NewStore()
	n := &Notification{ID: "test-2", Status: StatusQueued}
	s.Save(n)
	s.UpdateStatus("test-2", StatusSent)

	got, _ := s.Get("test-2")
	if got.Status != StatusSent {
		t.Errorf("expected sent, got %s", got.Status)
	}
}

func TestStore_List(t *testing.T) {
	s := NewStore()
	s.Save(&Notification{ID: "a"})
	s.Save(&Notification{ID: "b"})

	list := s.List()
	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}
}

// ── HTTP handler tests ────────────────────────────────────────────────────────

func newTestServer() *Server {
	store := NewStore()
	worker := NewWorker(store)
	// Do NOT start the worker goroutines — we control processing manually in tests
	return NewServer(store, worker)
}

func TestHealthz(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestSend_Valid(t *testing.T) {
	srv := newTestServer()
	body := `{"webhook_url":"http://example.com/hook","message":"hello","channel":"#general"}`
	req := httptest.NewRequest(http.MethodPost, "/notifications/send", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", rr.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp["id"] == "" {
		t.Error("expected non-empty id")
	}
	if resp["status"] != "queued" {
		t.Errorf("expected queued, got %s", resp["status"])
	}
}

func TestSend_MissingFields(t *testing.T) {
	srv := newTestServer()
	body := `{"channel":"#test"}` // missing webhook_url and message
	req := httptest.NewRequest(http.MethodPost, "/notifications/send", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestSend_InvalidJSON(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/notifications/send", bytes.NewBufferString("{bad"))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestSend_MethodNotAllowed(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/notifications/send", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestGetByID_Found(t *testing.T) {
	srv := newTestServer()

	// Enqueue one notification
	body := `{"webhook_url":"http://example.com/hook","message":"hi","channel":"ops"}`
	postReq := httptest.NewRequest(http.MethodPost, "/notifications/send", bytes.NewBufferString(body))
	postRr := httptest.NewRecorder()
	srv.ServeHTTP(postRr, postReq)

	var postResp map[string]string
	json.NewDecoder(postRr.Body).Decode(&postResp)
	id := postResp["id"]

	// Fetch by ID
	getReq := httptest.NewRequest(http.MethodGet, "/notifications/"+id, nil)
	getRr := httptest.NewRecorder()
	srv.ServeHTTP(getRr, getReq)

	if getRr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", getRr.Code)
	}

	var n Notification
	if err := json.NewDecoder(getRr.Body).Decode(&n); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n.ID != id {
		t.Errorf("expected id %s, got %s", id, n.ID)
	}
	if n.Status != StatusQueued {
		t.Errorf("expected queued, got %s", n.Status)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/notifications/doesnotexist", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestList(t *testing.T) {
	srv := newTestServer()

	// Post two notifications
	for _, msg := range []string{"first", "second"} {
		body := `{"webhook_url":"http://example.com","message":"` + msg + `"}`
		req := httptest.NewRequest(http.MethodPost, "/notifications/send", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.ServeHTTP(rr, req)
	}

	req := httptest.NewRequest(http.MethodGet, "/notifications", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	var list []Notification
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 notifications, got %d", len(list))
	}
}

// TestWorkerProcess verifies the worker transitions status from queued → sent.
func TestWorkerProcess(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	n := &Notification{
		ID:        "worker-test-1",
		Status:    StatusQueued,
		CreatedAt: now,
		UpdatedAt: now,
	}
	store.Save(n)

	w := &Worker{
		queue: make(chan string, 1),
		store: store,
	}

	// Override rng for deterministic 1-second sleep in tests — we set delay manually
	// by calling process() directly (bypasses rng sleep, but we need to set rng first)
	// Use a fixed seed so delay is always 1s (seed 0 → Intn(3)=0 → 1+0=1)
	w.rng = newRandSource(0)
	w.process("worker-test-1")

	got, _ := store.Get("worker-test-1")
	if got.Status != StatusSent {
		t.Errorf("expected sent after processing, got %s", got.Status)
	}
}

func TestWorkerProcess_MissingNotification(t *testing.T) {
	store := NewStore()
	w := &Worker{
		queue: make(chan string, 1),
		store: store,
		rng:   newRandSource(0),
	}
	// Should not panic
	w.process("nonexistent")
}

// TestStatusTransitions verifies the full lifecycle through the HTTP API and worker.
func TestStatusTransitions(t *testing.T) {
	store := NewStore()
	worker := &Worker{
		queue: make(chan string, 10),
		store: store,
		rng:   newRandSource(0),
	}
	srv := NewServer(store, worker)

	// Submit
	body := `{"webhook_url":"http://hooks.example.com/test","message":"status transition test","channel":"ci"}`
	postReq := httptest.NewRequest(http.MethodPost, "/notifications/send", bytes.NewBufferString(body))
	postRr := httptest.NewRecorder()
	srv.ServeHTTP(postRr, postReq)

	if postRr.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", postRr.Code)
	}

	var resp map[string]string
	json.NewDecoder(postRr.Body).Decode(&resp)
	id := resp["id"]

	// Should be queued
	n, _ := store.Get(id)
	if n.Status != StatusQueued {
		t.Errorf("expected queued, got %s", n.Status)
	}

	// Process synchronously
	worker.process(id)

	// Should now be sent
	n, _ = store.Get(id)
	if n.Status != StatusSent {
		t.Errorf("expected sent, got %s", n.Status)
	}

	// Verify via GET /notifications/{id}
	getReq := httptest.NewRequest(http.MethodGet, "/notifications/"+id, nil)
	getRr := httptest.NewRecorder()
	srv.ServeHTTP(getRr, getReq)
	var notif Notification
	json.NewDecoder(getRr.Body).Decode(&notif)
	if notif.Status != StatusSent {
		t.Errorf("expected sent via API, got %s", notif.Status)
	}
}

// TestList_MethodNotAllowed ensures non-GET on /notifications returns 405.
func TestList_MethodNotAllowed(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodDelete, "/notifications", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// TestGetByID_MethodNotAllowed ensures non-GET on /notifications/{id} returns 405.
func TestGetByID_MethodNotAllowed(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodPost, "/notifications/some-id", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}
