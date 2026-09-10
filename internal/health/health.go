// Package health implements liveness and readiness probes.
package health

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/LimeOnTop/voting-service/internal/httpjson"
)

// Dependency is a backing service the probe reports on.
type Dependency struct {
	Name     string
	Critical bool
	Check    func(context.Context) error
}

// Checker answers the probe endpoints.
type Checker struct {
	dependencies []Dependency
	timeout      time.Duration
	cacheFor     time.Duration

	mu       sync.Mutex
	cached   report
	cachedAt time.Time
}

type dependencyStatus struct {
	Name     string `json:"name"`
	Healthy  bool   `json:"healthy"`
	Critical bool   `json:"critical"`
	Error    string `json:"error,omitempty"`
}

type report struct {
	Status       string             `json:"status"`
	Dependencies []dependencyStatus `json:"dependencies"`
	ready        bool
}

// New builds a health checker with short-lived result caching.
func New(timeout, cacheFor time.Duration, dependencies ...Dependency) *Checker {
	return &Checker{dependencies: dependencies, timeout: timeout, cacheFor: cacheFor}
}

// Live reports process health only.
func (c *Checker) Live(w http.ResponseWriter, r *http.Request) {
	httpjson.WriteJSON(w, r, http.StatusOK, map[string]string{"status": "alive"})
}

// Ready reports whether this replica should receive traffic.
func (c *Checker) Ready(w http.ResponseWriter, r *http.Request) {
	current := c.evaluate(r.Context())
	status := http.StatusOK
	if !current.ready {
		status = http.StatusServiceUnavailable
	}
	httpjson.WriteJSON(w, r, status, current)
}

func (c *Checker) evaluate(ctx context.Context) report {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.cachedAt) < c.cacheFor {
		return c.cached
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	current := report{Status: "ready", ready: true, Dependencies: make([]dependencyStatus, 0, len(c.dependencies))}
	for _, dependency := range c.dependencies {
		status := dependencyStatus{Name: dependency.Name, Healthy: true, Critical: dependency.Critical}
		if err := dependency.Check(ctx); err != nil {
			status.Healthy = false
			status.Error = "unreachable"
			if dependency.Critical {
				current.ready = false
				current.Status = "unready"
			} else if current.ready {
				current.Status = "degraded"
			}
		}
		current.Dependencies = append(current.Dependencies, status)
	}

	c.cached = current
	c.cachedAt = time.Now()
	return current
}
