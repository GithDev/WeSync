package api

// BuildTime is injected at build time via -ldflags "-X wesync/internal/api.BuildTime=..."
// It is exposed via /api/status so you can confirm which binary is running.
var BuildTime = "dev"
