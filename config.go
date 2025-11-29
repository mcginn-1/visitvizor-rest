package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
)

// Config holds service configuration, similar to htmlery-rest's Config.
// For now we only need project ID (for Firestore), dev bearer token, and
// the imaging storage bucket for uploads.
type Config struct {
	ProjectID                    string
	DevBearer                    string
	ImagingBucket                string
	SignedURLServiceAccountEmail string
	SignedURLPrivateKey          string

	HealthcareLocation  string // e.g. "us-central1"
	HealthcareDatasetID string // "vv-dataset-1"
	HealthcareStoreID   string // "vv-dicom"
}

// serviceAccountCreds is a minimal view of a GCP service account JSON key.
type serviceAccountCreds struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

// loadUploadManagerCreds loads the upload-manager service account JSON from
// Google Secret Manager. The secret is expected to contain the raw JSON
// service account key for visitvizor-signed-urls@<project>.iam.gserviceaccount.com.
func loadUploadManagerCreds(ctx context.Context, projectID string) (string, string) {
	// Secret name is fixed for now; adjust if you need a different one per env.
	const secretID = "vv-1-a-upload-manager-credentials"

	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		log.Printf("loadUploadManagerCreds: failed to init Secret Manager client: %v", err)
		return "", ""
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("loadUploadManagerCreds: error closing Secret Manager client: %v", err)
		}
	}()

	name := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", projectID, secretID)
	resp, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{Name: name})
	if err != nil {
		log.Printf("loadUploadManagerCreds: AccessSecretVersion failed for %s: %v", name, err)
		return "", ""
	}
	if resp.Payload == nil || len(resp.Payload.Data) == 0 {
		log.Printf("loadUploadManagerCreds: secret %s has empty payload", name)
		return "", ""
	}

	var creds serviceAccountCreds
	if err := json.Unmarshal(resp.Payload.Data, &creds); err != nil {
		log.Printf("loadUploadManagerCreds: failed to unmarshal service account JSON: %v", err)
		return "", ""
	}

	if creds.ClientEmail == "" || creds.PrivateKey == "" {
		log.Printf("loadUploadManagerCreds: missing client_email or private_key in secret %s", name)
		return "", ""
	}

	return creds.ClientEmail, creds.PrivateKey
}

// LoadConfig reads configuration from environment variables, using
// the same names as the Python service where it makes sense, so
// deployment/env setup can be reused.
func LoadConfig() Config {
	projectID := os.Getenv("VISIT_VIZOR_PROJECT_ID")
	if projectID == "" {
		// Sensible default for local dev; change to your VisitVizor project.
		projectID = "vv-1-a"
	}

	devBearer := os.Getenv("AUTH_DEV_BEARER")

	// Imaging bucket used for uploads (kept private; access via backend only).
	imagingBucket := os.Getenv("VISIT_VIZOR_IMAGING_BUCKET")
	if imagingBucket == "" {
		imagingBucket = "vv-storage-vault"
	}

	ctx := context.Background()
	signedEmail, signedKey := loadUploadManagerCreds(ctx, projectID)
	
	fmt.Sprintf("DEBUG: signedEmail = %v", signedEmail)
	fmt.Sprintf("DEBUG: signedKey = %v", signedKey)

	healthLoc := os.Getenv("VISIT_VIZOR_HEALTHCARE_LOCATION")
	if healthLoc == "" {
		healthLoc = "us-central1"
	}

	dataset := os.Getenv("VISIT_VIZOR_HEALTHCARE_DATASET")
	if dataset == "" {
		dataset = "vv-dataset-1"
	}

	store := os.Getenv("VISIT_VIZOR_HEALTHCARE_DICOM_STORE")
	if store == "" {
		store = "vv-dicom"
	}


	return Config{
		ProjectID:     projectID,
		DevBearer:     devBearer,
		ImagingBucket: imagingBucket,

		SignedURLServiceAccountEmail: signedEmail,
		SignedURLPrivateKey:          signedKey,

		HealthcareLocation:  healthLoc,    // us-central1
		HealthcareDatasetID: dataset, // "vv-dataset-1"
		HealthcareStoreID:   store,   // "vv-dicom"
	}
}
