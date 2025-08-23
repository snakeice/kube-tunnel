package dashboard

import (
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/snakeice/kube-tunnel/internal/logger"
)

//go:embed templates/*
var dashboardFS embed.FS

// Dashboard serves the embedded real-time health monitoring dashboard.
type Dashboard struct {
	templates *template.Template
}

// NewDashboard creates a new dashboard instance.
func NewDashboard() (*Dashboard, error) {
	templates, err := template.ParseFS(dashboardFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &Dashboard{
		templates: templates,
	}, nil
}

// ServeDashboard serves the main dashboard page.
func (d *Dashboard) ServeDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := map[string]interface{}{
		"Timestamp": time.Now().Format(time.RFC3339),
		"Title":     "Kube-Tunnel Real-time Health Monitor",
	}

	if err := d.templates.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		logger.LogError("Failed to execute dashboard template", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// ServeDashboardAssets serves static assets (CSS, JS) for the dashboard.
func (d *Dashboard) ServeDashboardAssets(w http.ResponseWriter, r *http.Request) {
	// Extract the asset path from the URL
	assetPath := r.URL.Path[len("/dashboard/assets/"):]

	// Map asset paths to embedded files
	switch assetPath {
	case "dashboard.css":
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		d.serveCSS(w)
	case "dashboard.js":
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		d.serveJS(w)
	default:
		http.NotFound(w, r)
	}
}

// serveCSS serves the embedded CSS.
func (d *Dashboard) serveCSS(w http.ResponseWriter) {
	cssContent, err := dashboardFS.ReadFile("templates/dashboard.css")
	if err != nil {
		http.Error(w, "Failed to read CSS file", http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(cssContent); err != nil {
		http.Error(w, "Failed to write CSS", http.StatusInternalServerError)
		return
	}
}

// serveJS serves the embedded JavaScript.
func (d *Dashboard) serveJS(w http.ResponseWriter) {
	jsContent, err := dashboardFS.ReadFile("templates/dashboard.js")
	if err != nil {
		http.Error(w, "Failed to read JavaScript file", http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(jsContent); err != nil {
		http.Error(w, "Failed to write JavaScript", http.StatusInternalServerError)
		return
	}
}
