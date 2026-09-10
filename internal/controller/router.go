package controller

import (
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/LimeOnTop/voting-service/internal/entity"
	"github.com/LimeOnTop/voting-service/internal/health"
	"github.com/LimeOnTop/voting-service/internal/httpjson"
	"github.com/LimeOnTop/voting-service/internal/middleware"
	"github.com/go-chi/chi/v5"
)

// RouterConfig holds router middleware and probe settings.
type RouterConfig struct {
	AdminToken      string
	MaxRequestBytes int64
	RequestTimeout  time.Duration
	TrustedProxies  []netip.Prefix
	VoterCookie     middleware.VoterCookieConfig
	Logger          *slog.Logger
	Health          *health.Checker
}

// NewRouter builds the HTTP API router.
func NewRouter(handlers *Handlers, cfg RouterConfig) http.Handler {
	router := chi.NewRouter()

	router.Use(middleware.Correlation)
	router.Use(middleware.RealIP(cfg.TrustedProxies))
	router.Use(middleware.AccessLog(cfg.Logger))
	router.Use(middleware.Recover(cfg.Logger))
	router.Use(middleware.Timeout(cfg.RequestTimeout))
	router.Use(middleware.BodyLimit(cfg.MaxRequestBytes))

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpjson.WriteError(w, r, entity.NewError(entity.CodeNotFound, "endpoint not found"))
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpjson.WriteError(w, r, entity.NewError(entity.CodeInvalidRequest, "method not allowed for this endpoint"))
	})

	if cfg.Health != nil {
		router.Get("/health/live", cfg.Health.Live)
		router.Get("/health/ready", cfg.Health.Ready)
	}

	router.Route("/api/v1", func(api chi.Router) {
		api.Get("/polls/{poll_id}", handlers.GetPoll)

		api.With(middleware.VoterCookie(cfg.VoterCookie)).
			Post("/polls/{poll_id}/votes", handlers.CastVote)

		api.Route("/admin", func(admin chi.Router) {
			admin.Use(middleware.AdminAuth(cfg.AdminToken))
			admin.Get("/polls", handlers.ListPolls)
			admin.Post("/polls", handlers.CreatePoll)
			admin.Post("/polls/{poll_id}/activate", handlers.ActivatePoll)
			admin.Post("/polls/{poll_id}/finish", handlers.FinishPoll)
			admin.Get("/polls/{poll_id}/results", handlers.GetResults)
		})
	})

	return router
}
