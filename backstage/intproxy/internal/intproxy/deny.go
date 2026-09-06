package intproxy

import (
	"encoding/json"
	"net/http"
)

// writeDenial renders one refusal three ways, because git, gh and a gh graphql
// call each surface a different part of the response to the person reading it.
func (s *Server) writeDenial(w http.ResponseWriter, k kind, status int, msg string) {
	switch k {
	case kindREST:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message":           msg,
			"documentation_url": s.docsURL(),
		})
	case kindGraphQL:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errors": []map[string]string{{"message": msg}},
		})
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(msg + "\n"))
	}
}

func (s *Server) docsURL() string {
	if s.cfg.ConsoleURL == "" {
		return ""
	}
	return s.cfg.ConsoleURL + "/integrations"
}

func (s *Server) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("intproxy: " + s.ServerName() + " serves git over https, the github rest api under /api/v3, and graphql at /api/graphql\n"))
}
