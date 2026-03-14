package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ── Data model ────────────────────────────────────────────────────────────────

type Status string

const (
	StatusQueued Status = "queued"
	StatusSent   Status = "sent"
	StatusFailed Status = "failed"
)

type Notification struct {
	ID         string    `json:"id"`
	WebhookURL string    `json:"webhook_url"`
	Message    string    `json:"message"`
	Channel    string    `json:"channel"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ── In-memory store ───────────────────────────────────────────────────────────

type Store struct {
	mu    sync.RWMutex
	items map[string]*Notification
}

func NewStore() *Store {
	return &Store{items: make(map[string]*Notification)}
}

func (s *Store) Save(n *Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[n.ID] = n
}

func (s *Store) Get(id string) (*Notification, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.items[id]
	return n, ok
}

func (s *Store) List() []*Notification {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*Notification, 0, len(s.items))
	for _, n := range s.items {
		list = append(list, n)
	}
	return list
}

func (s *Store) UpdateStatus(id string, status Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.items[id]; ok {
		n.Status = status
		n.UpdatedAt = time.Now().UTC()
	}
}

// ── Worker pool ───────────────────────────────────────────────────────────────

const numWorkers = 3

type Worker struct {
	queue chan string
	store *Store
	rng   *rand.Rand
}

func NewWorker(store *Store) *Worker {
	return &Worker{
		queue: make(chan string, 256),
		store: store,
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (w *Worker) Start() {
	for i := 0; i < numWorkers; i++ {
		go w.run()
	}
}

func (w *Worker) Enqueue(id string) {
	w.queue <- id
}

func (w *Worker) run() {
	for id := range w.queue {
		w.process(id)
	}
}

func (w *Worker) process(id string) {
	n, ok := w.store.Get(id)
	if !ok {
		log.Printf("worker: notification %s not found", id)
		return
	}

	// Simulate work: sleep 1-3 seconds
	delay := time.Duration(1+w.rng.Intn(3)) * time.Second
	log.Printf("worker: processing %s (webhook=%s, delay=%s)", id, n.WebhookURL, delay)
	time.Sleep(delay)

	// Always mark as sent (simulation — no real HTTP POST)
	w.store.UpdateStatus(id, StatusSent)
	log.Printf("worker: notification %s sent", id)
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

type Server struct {
	store  *Store
	worker *Worker
	mux    *http.ServeMux
	idSeq  uint64
	idMu   sync.Mutex
}

func NewServer(store *Store, worker *Worker) *Server {
	s := &Server{
		store:  store,
		worker: worker,
		mux:    http.NewServeMux(),
	}
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/notifications/send", s.handleSend)
	s.mux.HandleFunc("/notifications/", s.handleGetByID)
	s.mux.HandleFunc("/notifications", s.handleList)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) nextID() string {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	s.idSeq++
	return fmt.Sprintf("notif-%d-%d", time.Now().UnixNano(), s.idSeq)
}

// GET /healthz
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok\n"))
}

// POST /notifications/send
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WebhookURL string `json:"webhook_url"`
		Message    string `json:"message"`
		Channel    string `json:"channel"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.WebhookURL == "" || req.Message == "" {
		http.Error(w, "webhook_url and message are required", http.StatusBadRequest)
		return
	}

	now := time.Now().UTC()
	n := &Notification{
		ID:         s.nextID(),
		WebhookURL: req.WebhookURL,
		Message:    req.Message,
		Channel:    req.Channel,
		Status:     StatusQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.store.Save(n)
	s.worker.Enqueue(n.ID)

	log.Printf("api: queued notification %s (channel=%s)", n.ID, n.Channel)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"id":     n.ID,
		"status": string(StatusQueued),
	})
}

// GET /notifications/{id}
func (s *Server) handleGetByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/notifications/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	n, ok := s.store.Get(id)
	if !ok {
		http.Error(w, "notification not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(n)
}

// GET /notifications
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	list := s.store.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// ── Entry point ───────────────────────────────────────────────────────────────

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	store := NewStore()
	worker := NewWorker(store)
	worker.Start()

	server := NewServer(store, worker)

	log.Printf("notifications-worker listening on :%s", port)
	if err := http.ListenAndServe(":"+port, server); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// newRandSource creates a *rand.Rand with a fixed seed — used in tests.
func newRandSource(seed int64) *rand.Rand {
	return rand.New(rand.NewSource(seed))
}
