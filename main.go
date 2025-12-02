package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"cloud.google.com/go/storage"

	"visitvizor-rest/dicomweb"
)

// Handlers holds dependencies shared by HTTP handlers.
type Handlers struct {
	Cfg     Config
	DB      *FirestoreDB
	Storage *storage.Client
	Dicom   *dicomweb.Client
}

func main() {
	cfg := LoadConfig()

	ctx := context.Background()
	fsdb, err := NewFirestoreDB(ctx, cfg.ProjectID)
	if err != nil {
		log.Fatalf("failed to init Firestore: %v", err)
	}
	defer func() {
		if err := fsdb.Close(); err != nil {
			log.Printf("error closing Firestore client: %v", err)
		}
	}()

	st, err := storage.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to init GCS storage client: %v", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Printf("error closing storage client: %v", err)
		}
	}()

	// DICOMweb client for Google Cloud Healthcare DICOM store
	dw, err := dicomweb.NewClient(
		ctx,
		cfg.ProjectID,
		cfg.HealthcareLocation,
		cfg.HealthcareDatasetID,
		cfg.HealthcareStoreID,
	)
	if err != nil {
		log.Fatalf("failed to init DICOMweb client: %v", err)
	}

	h := &Handlers{
		Cfg:     cfg,
		DB:      fsdb,
		Storage: st,
		Dicom:   dw,
	}

	mux := http.NewServeMux()

	// Auth routes (to be implemented to mirror routes_auth.py)
	mux.HandleFunc("/api/login", h.LoginHandler)

	// Accounts routes
	mux.HandleFunc("/api/accounts", h.AccountsHandler)      // POST create
	mux.HandleFunc("/api/accounts/me", h.AccountsMeHandler) // PUT update current user
	mux.HandleFunc("/api/accounts/", h.AccountsByIDHandler) // DELETE by id

	// Imaging / provider upload routes
	mux.HandleFunc("/api/imaging/provider-tokens", h.CreateProviderUploadTokenHandler)
	mux.HandleFunc("/api/imaging/provider/upload-sessions", h.ProviderCreateUploadSessionHandler)
	mux.HandleFunc("/api/imaging/provider/upload/", h.ProviderUploadFilesHandler)

	// Imaging study listing / detail routes
	mux.HandleFunc("/api/imaging/studies", h.ListImagingStudiesHandler)
	mux.HandleFunc("/api/imaging/studies/", h.ImagingStudyByIDHandler)

	// Longitudinal (scan-over-time) endpoints
	mux.HandleFunc("/api/imaging/longitudinal/index", h.LongitudinalIndexHandler)
	mux.HandleFunc("/api/imaging/longitudinal/index-status", h.LongitudinalIndexStatusHandler)
	mux.HandleFunc("/api/imaging/longitudinal/resolve-point", h.LongitudinalResolvePointHandler)

	// Minimal DICOMweb-style proxy for OHIF / other viewers
	mux.HandleFunc("/api/dicomweb/studies/", h.DicomWebStudiesHandler)

	// Internal Pub/Sub endpoint for DICOM ingest worker
	mux.HandleFunc("/internal/pubsub/dicom-ingest", h.PubSubDicomIngestHandler)
	// Get upload session object
	mux.HandleFunc("/api/imaging/provider/upload-sessions/", h.ProviderGetUploadSessionHandler)

	//mux.HandleFunc("/api/imaging/provider/upload-sessions/", h.ProviderGetUploadSessionHandler)

	// Obtains upload-url for gcs bucket upload session
	mux.HandleFunc("/api/imaging/upload-url", h.ProviderUploadURLHandler)

	mux.HandleFunc("/api/imaging/user/upload-sessions", h.UserCreateUploadSessionHandler)

	//// Indexing for point-over-time
	//mux.HandleFunc("/api/imaging/longitudinal/index", h.LongitudinalIndexHandler)
	//mux.HandleFunc("/api/imaging/longitudinal/index-status", h.LongitudinalIndexStatusHandler)

	//// Internal Pub/Sub endpoint for DICOM ingest worker
	//mux.HandleFunc("/internal/pubsub/dicom-ingest", h.PubSubDicomIngestHandler)

	addr := ":8080"
	server := &http.Server{
		Addr:    addr,
		Handler: withCORS(mux),
	}

	go func() {
		log.Printf("VisitVizor REST server listening on %s (project=%s)", addr, cfg.ProjectID)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server failed: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down server...")
	if err := server.Shutdown(context.Background()); err != nil {
		log.Printf("server shutdown error: %v", err)
	}
}
