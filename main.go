package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// =============================================================================
// DATA MODEL
// =============================================================================

// Container represents a deployable service/application.
type Container struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Secret string `json:"secret,omitempty"`
}

// APIResponse is the standard JSON response format.
type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// DeployResult contains the output from a deployment operation.
type DeployResult struct {
	ContainerID string `json:"container_id"`
	Output      string `json:"output"`
	Duration    string `json:"duration"`
}

// =============================================================================
// STORAGE (JSON FILE WITH MUTEX)
// =============================================================================

const dataFile = "containers.json"

var (
	storageMu sync.RWMutex
	// deployMu holds per-container mutexes to prevent parallel deploys
	deployMu sync.Map
)

// getDeployMutex returns a mutex for the given container ID.
func getDeployMutex(id string) *sync.Mutex {
	mu, _ := deployMu.LoadOrStore(id, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// loadContainers reads all containers from the JSON file.
func loadContainers() ([]Container, error) {
	storageMu.RLock()
	defer storageMu.RUnlock()

	// Create file if it doesn't exist
	if _, err := os.Stat(dataFile); os.IsNotExist(err) {
		if err := os.WriteFile(dataFile, []byte("[]"), 0644); err != nil {
			return nil, fmt.Errorf("failed to create data file: %w", err)
		}
		return []Container{}, nil
	}

	data, err := os.ReadFile(dataFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read data file: %w", err)
	}

	var containers []Container
	if err := json.Unmarshal(data, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse data file: %w", err)
	}

	return containers, nil
}

// saveContainers writes all containers to the JSON file.
func saveContainers(containers []Container) error {
	storageMu.Lock()
	defer storageMu.Unlock()

	data, err := json.MarshalIndent(containers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal containers: %w", err)
	}

	if err := os.WriteFile(dataFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write data file: %w", err)
	}

	return nil
}

// findContainer finds a container by ID from a list.
func findContainer(containers []Container, id string) (*Container, int) {
	for i, c := range containers {
		if c.ID == id {
			return &containers[i], i
		}
	}
	return nil, -1
}

// =============================================================================
// VALIDATION
// =============================================================================

// validatePath ensures the path is safe and absolute.
func validatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}

	// Must be absolute path
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}

	// Prevent directory traversal
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("path contains invalid characters")
	}

	return nil
}

// validateContainer validates container fields.
func validateContainer(c *Container) error {
	if c.ID == "" {
		return fmt.Errorf("id is required")
	}

	// ID should be alphanumeric with dashes/underscores only
	for _, ch := range c.ID {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return fmt.Errorf("id contains invalid characters (use alphanumeric, dash, underscore)")
		}
	}

	if err := validatePath(c.Path); err != nil {
		return err
	}

	if c.Secret == "" {
		return fmt.Errorf("secret is required")
	}

	if len(c.Secret) < 8 {
		return fmt.Errorf("secret must be at least 8 characters")
	}

	return nil
}

// =============================================================================
// MIDDLEWARE
// =============================================================================

// recoveryMiddleware catches panics and returns 500 error.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %v", err)
				sendError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs all requests.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[%s] %s %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
		log.Printf("[%s] %s completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// =============================================================================
// HTTP HELPERS
// =============================================================================

// sendJSON sends a JSON response.
func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// sendSuccess sends a successful JSON response.
func sendSuccess(w http.ResponseWriter, data interface{}) {
	sendJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
}

// sendError sends an error JSON response.
func sendError(w http.ResponseWriter, status int, message string) {
	sendJSON(w, status, APIResponse{Success: false, Error: message})
}

// parseJSONBody parses the JSON body into the target struct.
func parseJSONBody(r *http.Request, target interface{}) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}
	defer r.Body.Close()

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}

	return nil
}

// =============================================================================
// HANDLERS: CRUD
// =============================================================================

// handleCreateContainer creates a new container.
func handleCreateContainer(w http.ResponseWriter, r *http.Request) {
	var input Container
	if err := parseJSONBody(r, &input); err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validateContainer(&input); err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	containers, err := loadContainers()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Check for duplicate ID
	if existing, _ := findContainer(containers, input.ID); existing != nil {
		sendError(w, http.StatusConflict, "container with this ID already exists")
		return
	}

	containers = append(containers, input)
	if err := saveContainers(containers); err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[CREATED] Container %s at %s", input.ID, input.Path)

	// Return without secret
	output := Container{ID: input.ID, Path: input.Path}
	sendJSON(w, http.StatusCreated, APIResponse{Success: true, Data: output})
}

// handleListContainers lists all containers (without secrets).
func handleListContainers(w http.ResponseWriter, r *http.Request) {
	containers, err := loadContainers()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Strip secrets from response
	output := make([]Container, len(containers))
	for i, c := range containers {
		output[i] = Container{ID: c.ID, Path: c.Path}
	}

	sendSuccess(w, output)
}

// handleGetContainer gets a single container by ID.
func handleGetContainer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	containers, err := loadContainers()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	container, _ := findContainer(containers, id)
	if container == nil {
		sendError(w, http.StatusNotFound, "container not found")
		return
	}

	// Return without secret
	output := Container{ID: container.ID, Path: container.Path}
	sendSuccess(w, output)
}

// handleUpdateContainer updates an existing container.
func handleUpdateContainer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	var input Container
	if err := parseJSONBody(r, &input); err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Set ID from URL (ignore body ID)
	input.ID = id

	if err := validateContainer(&input); err != nil {
		sendError(w, http.StatusBadRequest, err.Error())
		return
	}

	containers, err := loadContainers()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, idx := findContainer(containers, id)
	if idx == -1 {
		sendError(w, http.StatusNotFound, "container not found")
		return
	}

	containers[idx] = input
	if err := saveContainers(containers); err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[UPDATED] Container %s", id)

	// Return without secret
	output := Container{ID: input.ID, Path: input.Path}
	sendSuccess(w, output)
}

// handleDeleteContainer deletes a container by ID.
func handleDeleteContainer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	containers, err := loadContainers()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_, idx := findContainer(containers, id)
	if idx == -1 {
		sendError(w, http.StatusNotFound, "container not found")
		return
	}

	// Remove from slice
	containers = append(containers[:idx], containers[idx+1:]...)
	if err := saveContainers(containers); err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("[DELETED] Container %s", id)
	sendSuccess(w, map[string]string{"deleted": id})
}

// =============================================================================
// HANDLERS: DEPLOY
// =============================================================================

// handleDeploy triggers a deployment for a container.
func handleDeploy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	secret := r.URL.Query().Get("secret")

	if secret == "" {
		sendError(w, http.StatusUnauthorized, "secret is required")
		return
	}

	containers, err := loadContainers()
	if err != nil {
		sendError(w, http.StatusInternalServerError, err.Error())
		return
	}

	container, _ := findContainer(containers, id)
	if container == nil {
		sendError(w, http.StatusNotFound, "container not found")
		return
	}

	// Verify secret
	if container.Secret != secret {
		log.Printf("[AUTH FAILED] Deploy attempt for %s with invalid secret", id)
		sendError(w, http.StatusForbidden, "invalid secret")
		return
	}

	// Acquire per-container mutex (prevent parallel deploys to same container)
	mu := getDeployMutex(id)
	if !mu.TryLock() {
		sendError(w, http.StatusConflict, "deployment already in progress for this container")
		return
	}
	defer mu.Unlock()

	log.Printf("[DEPLOY START] Container %s at %s", id, container.Path)
	start := time.Now()

	// Execute deployment
	output, err := executeDeploy(container.Path)
	duration := time.Since(start)

	if err != nil {
		log.Printf("[DEPLOY FAILED] Container %s: %v", id, err)
		sendError(w, http.StatusInternalServerError, fmt.Sprintf("deployment failed: %s\n\nOutput:\n%s", err.Error(), output))
		return
	}

	log.Printf("[DEPLOY SUCCESS] Container %s completed in %v", id, duration)

	result := DeployResult{
		ContainerID: id,
		Output:      output,
		Duration:    duration.String(),
	}
	sendSuccess(w, result)
}

// executeDeploy runs the deployment commands in the specified directory.
func executeDeploy(path string) (string, error) {
	var output bytes.Buffer

	// Verify path exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("path does not exist: %s", path)
	}

	commands := []struct {
		name string
		args []string
	}{
		{"git", []string{"reset", "--hard"}},
		{"git", []string{"clean", "-fd"}},
		{"git", []string{"pull"}},
		{"docker", []string{"compose", "up", "-d", "--build", "--force-recreate"}},
	}

	for _, cmd := range commands {
		output.WriteString(fmt.Sprintf("\n=== %s %s ===\n", cmd.name, strings.Join(cmd.args, " ")))

		c := exec.Command(cmd.name, cmd.args...)
		c.Dir = path
		c.Stdout = &output
		c.Stderr = &output

		if err := c.Run(); err != nil {
			return output.String(), fmt.Errorf("command failed: %s %s: %w", cmd.name, strings.Join(cmd.args, " "), err)
		}
	}

	return output.String(), nil
}

// =============================================================================
// HANDLERS: HEALTH
// =============================================================================

// handleHealth returns a simple health check response.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	sendSuccess(w, map[string]string{
		"status": "healthy",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// =============================================================================
// MAIN
// =============================================================================

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	r := mux.NewRouter()

	// Apply middleware
	r.Use(recoveryMiddleware)
	r.Use(loggingMiddleware)

	// Health endpoint
	r.HandleFunc("/health", handleHealth).Methods("GET")

	// CRUD endpoints
	r.HandleFunc("/containers", handleCreateContainer).Methods("POST")
	r.HandleFunc("/containers", handleListContainers).Methods("GET")
	r.HandleFunc("/containers/{id}", handleGetContainer).Methods("GET")
	r.HandleFunc("/containers/{id}", handleUpdateContainer).Methods("PUT")
	r.HandleFunc("/containers/{id}", handleDeleteContainer).Methods("DELETE")

	// Deploy endpoint
	r.HandleFunc("/deploy/{id}", handleDeploy).Methods("POST")

	log.Printf("Deploy server starting on port %s", port)
	log.Printf("Data file: %s", dataFile)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 300 * time.Second, // Long timeout for deployments
		IdleTimeout:  120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
