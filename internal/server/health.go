package server

import (
	"context"
	"net/http"
	"time"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	st, ok := s.store.(interface{ Health(context.Context) error })
	if !ok || st.Health(ctx) != nil {
		jsonResponse(w, map[string]string{"status": "unavailable"}, http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, map[string]string{"status": "ok"}, http.StatusOK)
}
