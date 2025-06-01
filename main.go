package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// Structure pour un site à surveiller
type Site struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Structure pour le statut d'un site
type SiteStatus struct {
	Site         Site      `json:"site"`
	IsUp         bool      `json:"is_up"`
	ResponseTime int64     `json:"response_time_ms"`
	StatusCode   int       `json:"status_code"`
	LastChecked  time.Time `json:"last_checked"`
	Error        string    `json:"error,omitempty"`
}

// Stockage en mémoire pour commencer
var (
	sites       []Site
	statuses    []SiteStatus
	statusMutex sync.RWMutex
)

func main() {
	// Initialiser avec quelques sites de test
	initSites()

	// Démarrer la surveillance en arrière-plan
	go startMonitoring()

	// Configuration des routes API
	http.HandleFunc("/api/sites", handleSites)
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/health", handleHealth)

	// Ajouter CORS pour React
	http.HandleFunc("/", corsMiddleware)

	fmt.Println("🚀 Site Monitor API démarrée sur http://localhost:8080")
	fmt.Println("📊 Endpoints disponibles:")
	fmt.Println("   GET /api/sites   - Liste des sites")
	fmt.Println("   GET /api/status  - Statut de tous les sites")
	fmt.Println("   GET /api/health  - Health check de l'API")

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func initSites() {
	data, err := os.ReadFile("config/sites.json")
	if err != nil {
		log.Fatal("❌ Impossible de lire config/sites.json:", err)
	}

	if err := json.Unmarshal(data, &sites); err != nil {
		log.Fatal("❌ Erreur JSON dans config/sites.json:", err)
	}

	log.Printf("✅ %d sites chargés", len(sites))
}

func startMonitoring() {
	ticker := time.NewTicker(60 * time.Second) // Vérifier toutes les 30 secondes
	defer ticker.Stop()

	// Première vérification immédiate
	checkAllSites()

	for range ticker.C {
		checkAllSites()
	}
}

func checkAllSites() {
	checkTime := time.Now()
	fmt.Printf("🔍 Vérification des sites - %s\n", checkTime.Format("2006-01-02 15:04:05"))

	var wg sync.WaitGroup
	newStatuses := make([]SiteStatus, len(sites))

	for i, site := range sites {
		wg.Add(1)
		go func(index int, s Site) {
			defer wg.Done()
			status := checkSite(s)
			newStatuses[index] = status

			// Log du résultat
			statusIcon := "✅"
			errorMsg := ""
			if !status.IsUp {
				statusIcon = "❌"
				if status.Error != "" {
					errorMsg = fmt.Sprintf(" (%s)", status.Error)
				}
			}
			fmt.Printf("   %s %s - [%s] - %dms%s\n",
				statusIcon,
				s.Name,
				status.LastChecked.Format("15:04:05"),
				status.ResponseTime,
				errorMsg)
		}(i, site)
	}

	wg.Wait()

	// Mettre à jour le statut global
	statusMutex.Lock()
	statuses = newStatuses
	statusMutex.Unlock()
}

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
		status.IsUp = resp.StatusCode >= 200 && resp.StatusCode < 400
		status.StatusCode = resp.StatusCode
		resp.Body.Close()
	}

	return status
}

// Middleware CORS pour permettre les requêtes depuis React
func corsMiddleware(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Router vers les autres handlers
	switch r.URL.Path {
	case "/api/sites":
		handleSites(w, r)
	case "/api/status":
		handleStatus(w, r)
	case "/api/health":
		handleHealth(w, r)
	default:
		http.NotFound(w, r)
	}
}

func handleSites(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sites)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	statusMutex.RLock()
	defer statusMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now(),
		"uptime":    "running",
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// func saveStatusToFile() {
// 	statusMutex.RLock()
// 	defer statusMutex.RUnlock()

// 	file, err := os.Create("status.json")
// 	if err != nil {
// 		log.Printf("Erreur lors de la sauvegarde: %v", err)
// 		return
// 	}
// 	defer file.Close()

// 	encoder := json.NewEncoder(file)
// 	encoder.SetIndent("", "  ")
// 	encoder.Encode(statuses)
// }
