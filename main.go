package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Site représente un site à surveiller
type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// SiteStatus contient le statut d’un site après vérification
type SiteStatus struct {
	Site         Site      `json:"site"`
	IsUp         bool      `json:"is_up"`
	ResponseTime int64     `json:"response_time_ms"`
	StatusCode   int       `json:"status_code"`
	LastChecked  time.Time `json:"last_checked"`
	Error        string    `json:"error,omitempty"`
}

var (
	sites       []Site
	statuses    []SiteStatus
	statusMutex sync.RWMutex
	startTime   = time.Now()
)

func main() {
	// 1. Charger la configuration des sites
	if err := loadSites("config/sites.json"); err != nil {
		log.Fatalf("❌ Impossible de charger les sites : %v", err)
	}
	log.Printf("✅ %d site(s) à surveiller\n", len(sites))

	// 2. Initialiser le slice des statuses avec des valeurs par défaut
	initializeEmptyStatuses()

	// 3. Démarrer le monitoring en arrière-plan
	ctx, cancel := context.WithCancel(context.Background())
	go startMonitoring(ctx)

	// 4. Construire le ServeMux et ajouter les handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/api/sites", recoveryMiddleware(handleSites))
	mux.HandleFunc("/api/status", recoveryMiddleware(handleStatus))
	mux.HandleFunc("/api/health", recoveryMiddleware(handleHealth))

	// 5. Envelopper dans le middleware CORS
	handlerWithCORS := corsMiddleware(mux)

	// 6. Récupérer le port depuis l'environnement
	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("La variable d’environnement PORT n’est pas définie")
	}

	// 7. Configurer le serveur HTTP avec timeouts
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      handlerWithCORS,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 8. Démarrer le serveur dans une goroutine
	go func() {
		log.Printf("🚀 Site Monitor API démarrée sur le port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Le serveur HTTP s’est arrêté de manière inattendue : %v", err)
		}
	}()

	// 9. Attendre un signal d’arrêt (Ctrl+C, SIGINT, SIGTERM)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("🔔 Signal d'arrêt reçu, arrêt propre du serveur...")

	// 10. Annuler le contexte du monitoring
	cancel()

	// 11. Shutdown du serveur avec un timeout de 5 secondes
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(ctxShutdown); err != nil {
		log.Fatalf("🛑 Erreur lors de l’arrêt du serveur : %v", err)
	}
	log.Println("✅ Serveur arrêté proprement")
}

// loadSites lit le fichier JSON et remplit le slice sites
func loadSites(filepath string) error {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &sites); err != nil {
		return err
	}
	return nil
}

// initializeEmptyStatuses crée un slice de SiteStatus "vide" pour chaque site
func initializeEmptyStatuses() {
	statuses = make([]SiteStatus, len(sites))
	now := time.Now()
	for i, s := range sites {
		statuses[i] = SiteStatus{
			Site:         s,
			IsUp:         false,
			ResponseTime: 0,
			StatusCode:   0,
			LastChecked:  now,
			Error:        "En attente de la première vérification",
		}
	}
}

// startMonitoring lance un ticker qui exécute checkAllSites toutes les 60 secondes
func startMonitoring(ctx context.Context) {
	// Première exécution immédiate
	checkAllSites()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Monitoring arrêté (contexte annulé)")
			return
		case t := <-ticker.C:
			log.Printf("🔍 Nouvelle passe de vérification à %s\n", t.Format("2006-01-02 15:04:05"))
			checkAllSites()
		}
	}
}

// checkAllSites parcourt tous les sites en parallèle et met à jour le slice statuses
func checkAllSites() {
	var wg sync.WaitGroup
	newStatuses := make([]SiteStatus, len(sites))

	for i, site := range sites {
		wg.Add(1)
		go func(idx int, s Site) {
			defer wg.Done()
			status := checkSite(s)
			newStatuses[idx] = status

			// Log synthétique
			icon := "✅"
			if !status.IsUp {
				icon = "❌"
			}
			log.Printf("   %s %-20s → %4dms (code %d) [%s] %s",
				icon,
				s.Name,
				status.ResponseTime,
				status.StatusCode,
				status.LastChecked.Format("15:04:05"),
				status.Error,
			)
		}(i, site)
	}

	wg.Wait()

	// Verrouiller pour remplacer l’ancien slice
	statusMutex.Lock()
	statuses = newStatuses
	statusMutex.Unlock()
}

// checkSite effectue une requête GET vers site.URL et renvoie un SiteStatus
func checkSite(site Site) SiteStatus {
	start := time.Now()

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(site.URL)
	duration := time.Since(start).Milliseconds()

	status := SiteStatus{
		Site:         site,
		ResponseTime: duration,
		LastChecked:  time.Now(),
	}

	if err != nil {
		status.IsUp = false
		status.Error = err.Error()
		status.StatusCode = 0
	} else {
		status.StatusCode = resp.StatusCode
		status.IsUp = resp.StatusCode >= 200 && resp.StatusCode < 400
		resp.Body.Close()
	}
	return status
}

// --- Handlers HTTP ---

// handleSites renvoie la liste des sites (sans métadonnées)
func handleSites(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sites)
}

// handleStatus renvoie le statut actuel de tous les sites
func handleStatus(w http.ResponseWriter, r *http.Request) {
	statusMutex.RLock()
	defer statusMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

// handleHealth renvoie un JSON simple pour le healthcheck
func handleHealth(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(startTime).String()
	health := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().UTC(),
		"uptime":    uptime,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// recoveryMiddleware intercepte une panic dans un handler et renvoie un 500
func recoveryMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("⚠️ Panic interceptée dans handler: %v", rec)
				http.Error(w, "Erreur interne du serveur", http.StatusInternalServerError)
			}
		}()
		next(w, r)
	}
}

// corsMiddleware enveloppe un http.Handler et ajoute les en-têtes CORS
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
