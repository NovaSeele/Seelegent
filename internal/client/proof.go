package client

import (
	"context"
	"net/http"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Lanes is GET /lanes: the lane claims held in the repository of cwd.
func (c *Client) Lanes(ctx context.Context, cwd string) (api.LanesView, error) {
	var v api.LanesView
	_, err := c.Do(ctx, http.MethodGet, "/lanes"+query("cwd", cwd), nil, &v)
	return v, err
}

// Contention is GET /contention: the files several tasks of the repository of cwd changed.
func (c *Client) Contention(ctx context.Context, cwd string) (api.ContentionView, error) {
	var v api.ContentionView
	_, err := c.Do(ctx, http.MethodGet, "/contention"+query("cwd", cwd), nil, &v)
	return v, err
}

// ProofIndex is GET /proof/index.
func (c *Client) ProofIndex(ctx context.Context, cwd string) (api.ProofIndex, error) {
	var v api.ProofIndex
	_, err := c.Do(ctx, http.MethodGet, "/proof/index"+query("cwd", cwd), nil, &v)
	return v, err
}

// ProofMatrix is GET /proof/matrix.
func (c *Client) ProofMatrix(ctx context.Context, cwd, feature string) (api.ProofMatrix, error) {
	var v api.ProofMatrix
	_, err := c.Do(ctx, http.MethodGet, "/proof/matrix"+query("cwd", cwd, "feature", feature), nil, &v)
	return v, err
}

// ProofRun is POST /proof/run: starts a regression run.
func (c *Client) ProofRun(ctx context.Context, req api.ProofRunRequest) (api.ProofRun, error) {
	var v api.ProofRun
	_, err := c.Do(ctx, http.MethodPost, "/proof/run", req, &v)
	return v, err
}

// ProofRunStatus is GET /proof/runs/{id}.
func (c *Client) ProofRunStatus(ctx context.Context, cwd, id string) (api.ProofRun, error) {
	var v api.ProofRun
	_, err := c.Do(ctx, http.MethodGet, "/proof/runs/"+seg(id)+query("cwd", cwd), nil, &v)
	return v, err
}

// ProofCancelRun is POST /proof/runs/{id}/cancel.
func (c *Client) ProofCancelRun(ctx context.Context, cwd, id string) (api.ProofRun, error) {
	var v api.ProofRun
	_, err := c.Do(ctx, http.MethodPost, "/proof/runs/"+seg(id)+"/cancel", map[string]string{"cwd": cwd}, &v)
	return v, err
}

// ProofStats is GET /proof/stats (since: RFC 3339, "" for all).
func (c *Client) ProofStats(ctx context.Context, cwd, since string) (api.ProofStats, error) {
	var v api.ProofStats
	_, err := c.Do(ctx, http.MethodGet, "/proof/stats"+query("cwd", cwd, "since", since), nil, &v)
	return v, err
}
