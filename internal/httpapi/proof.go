package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Proof is the proof ledger and its regression runs (proof.Service).
type Proof interface {
	Matrix(ctx context.Context, cwd, feature string) (api.ProofMatrix, error)
	Index(ctx context.Context, cwd string) (api.ProofIndex, error)
	StartRun(ctx context.Context, req api.ProofRunRequest) (api.ProofRun, error)
	RunStatus(ctx context.Context, cwd, id string) (api.ProofRun, error)
	CancelRun(ctx context.Context, cwd, id string) (api.ProofRun, error)
}

// Gates is the gate part of the task engine (task.Service).
type Gates interface {
	Lanes(ctx context.Context, cwd string) (api.LanesView, error)
	Contention(ctx context.Context, cwd string) (api.ContentionView, error)
	ProofStats(ctx context.Context, cwd string, since time.Time) (api.ProofStats, error)
}

// proofRoutes are the M13 endpoints: lanes, contention and the proof ledger.
func (s *Server) proofRoutes() {
	if s.o.Gates != nil {
		s.handle("GET /lanes", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
			cwd, err := requiredCwd(r)
			if err != nil {
				return 0, nil, err
			}
			v, err := s.o.Gates.Lanes(r.Context(), cwd)
			return http.StatusOK, v, err
		})
		s.handle("GET /contention", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
			cwd, err := requiredCwd(r)
			if err != nil {
				return 0, nil, err
			}
			v, err := s.o.Gates.Contention(r.Context(), cwd)
			return http.StatusOK, v, err
		})
		s.handle("GET /proof/stats", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
			cwd, err := optionalCwd(r.URL.Query().Get("cwd"))
			if err != nil {
				return 0, nil, err
			}
			var since time.Time
			if raw := r.URL.Query().Get("since"); raw != "" {
				if since, err = time.Parse(time.RFC3339, raw); err != nil {
					return 0, nil, invalid("since must be an RFC 3339 time").withField("since")
				}
			}
			v, err := s.o.Gates.ProofStats(r.Context(), cwd, since)
			return http.StatusOK, v, err
		})
	}
	if s.o.Proof == nil {
		return
	}
	s.handle("GET /proof/index", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := requiredCwd(r)
		if err != nil {
			return 0, nil, err
		}
		v, err := s.o.Proof.Index(r.Context(), cwd)
		return http.StatusOK, v, err
	})
	s.handle("GET /proof/matrix", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := requiredCwd(r)
		if err != nil {
			return 0, nil, err
		}
		feature := r.URL.Query().Get("feature")
		if feature == "" {
			return 0, nil, invalid("feature is required").withField("feature")
		}
		v, err := s.o.Proof.Matrix(r.Context(), cwd, feature)
		return http.StatusOK, v, err
	})
	s.handle("POST /proof/run", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.ProofRunRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		cwd, err := absDir(req.Cwd)
		if err != nil {
			return 0, nil, err
		}
		req.Cwd = cwd
		v, err := s.o.Proof.StartRun(r.Context(), req)
		return http.StatusAccepted, v, err
	})
	s.handle("GET /proof/runs/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := requiredCwd(r)
		if err != nil {
			return 0, nil, err
		}
		v, err := s.o.Proof.RunStatus(r.Context(), cwd, r.PathValue("id"))
		return http.StatusOK, v, err
	})
	s.handle("POST /proof/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req struct {
			Cwd string `json:"cwd"`
		}
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if req.Cwd == "" {
			req.Cwd = r.URL.Query().Get("cwd")
		}
		cwd, err := absDir(req.Cwd)
		if err != nil {
			return 0, nil, err
		}
		v, err := s.o.Proof.CancelRun(r.Context(), cwd, r.PathValue("id"))
		return http.StatusOK, v, err
	})
}

// requiredCwd reads the cwd query parameter: an absolute folder.
func requiredCwd(r *http.Request) (string, error) {
	raw := r.URL.Query().Get("cwd")
	if raw == "" {
		return "", invalid("cwd is required").withField("cwd")
	}
	return absDir(raw)
}

// PublishProofRun sends a proof.run frame (the proof service's Publish).
func (s *Server) PublishProofRun(run api.ProofRun) {
	s.publish(api.StreamProofRun, run)
}
