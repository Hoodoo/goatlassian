// Package web serves goatlassian's local web UI and its JSON API.
package web

import (
	"net/http"
	"time"

	"goatlassian/internal/config"
	"goatlassian/internal/store"
)

// Server is the web UI.
type Server struct {
	st   *store.Store
	cfg  config.Config
	home string
	mux  *http.ServeMux
}

// New builds the server.
func New(st *store.Store, cfg config.Config, home string) *Server {
	s := &Server{st: st, cfg: cfg, home: home, mux: http.NewServeMux()}
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// SnapshotLoop records snapshots periodically.
func (s *Server) SnapshotLoop(every time.Duration) {}
