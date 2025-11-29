package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	healthcare "google.golang.org/api/healthcare/v1"
)

// DicomWebClient provides convenience helpers for DICOMweb operations
// (retrieve, delete, metadata) against the configured Healthcare DICOM
// store. It is intentionally not wired into the HTTP server; you can use
// it from small CLIs, tests, or ad-hoc tooling without impacting
// production request handling.
type DicomWebClient struct {
	cfg Config
	svc *healthcare.Service
}

// NewDicomWebClient creates a DICOMweb client using the same Config and
// Application Default Credentials the rest of the service uses.
func NewDicomWebClient(ctx context.Context, cfg Config) (*DicomWebClient, error) {
	svc, err := healthcare.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("healthcare.NewService: %w", err)
	}
	return &DicomWebClient{
		cfg: cfg,
		svc: svc,
	}, nil
}

// dicomStoreParent builds the parent path used for DICOMweb calls,
// populated from Config (project, location, dataset, dicom store).
func (c *DicomWebClient) dicomStoreParent() string {
	return fmt.Sprintf(
		"projects/%s/locations/%s/datasets/%s/dicomStores/%s",
		c.cfg.ProjectID,
		c.cfg.HealthcareLocation,
		c.cfg.HealthcareDatasetID,
		c.cfg.HealthcareStoreID,
	)
}

// RetrieveStudyToFile retrieves an entire study (all instances) via
// DICOMweb and saves the multipart response to outputFile.
//
//   - studyUID: DICOM Study Instance UID like
//     "1.3.6.1.4.1.11129.5.5.111396399857604".
//   - outputFile: destination path, usually ending with ".multipart".
func (c *DicomWebClient) RetrieveStudyToFile(ctx context.Context, studyUID, outputFile string) error {
	if studyUID == "" {
		return fmt.Errorf("studyUID is required")
	}

	parent := c.dicomStoreParent()
	dicomWebPath := fmt.Sprintf("studies/%s", studyUID)

	studiesSvc := c.svc.Projects.Locations.Datasets.DicomStores.Studies

	resp, err := studiesSvc.RetrieveStudy(parent, dicomWebPath).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("RetrieveStudy: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return fmt.Errorf("RetrieveStudy: status %d %s", resp.StatusCode, resp.Status)
	}

	f, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("os.Create(%s): %w", outputFile, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("io.Copy to %s: %w", outputFile, err)
	}

	fmt.Fprintf(os.Stdout, "Study %q retrieved to %s\n", studyUID, outputFile)
	return nil
}

// DeleteStudy deletes an entire study (all instances) by StudyInstanceUID
// using the DICOMweb delete endpoint.
func (c *DicomWebClient) DeleteStudy(ctx context.Context, studyUID string) error {
	if studyUID == "" {
		return fmt.Errorf("studyUID is required")
	}

	parent := c.dicomStoreParent()
	dicomWebPath := fmt.Sprintf("studies/%s", studyUID)

	studiesSvc := c.svc.Projects.Locations.Datasets.DicomStores.Studies

	if _, err := studiesSvc.Delete(parent, dicomWebPath).Context(ctx).Do(); err != nil {
		return fmt.Errorf("DeleteStudy: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Deleted study %q\n", studyUID)
	return nil
}

// StudyMetadataJSON fetches the DICOMweb /metadata for a study and returns
// the pretty-printed JSON bytes so you can inspect tags/series/instances
// easily in tooling or tests.
func (c *DicomWebClient) StudyMetadataJSON(ctx context.Context, studyUID string) ([]byte, error) {
	if studyUID == "" {
		return nil, fmt.Errorf("studyUID is required")
	}

	parent := c.dicomStoreParent()
	dicomWebPath := fmt.Sprintf("studies/%s/metadata", studyUID)

	studiesSvc := c.svc.Projects.Locations.Datasets.DicomStores.Studies

	resp, err := studiesSvc.RetrieveMetadata(parent, dicomWebPath).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("RetrieveMetadata: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode > 299 {
		return nil, fmt.Errorf("RetrieveMetadata: status %d %s", resp.StatusCode, resp.Status)
	}

	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode metadata JSON: %w", err)
	}

	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("pretty-print JSON: %w", err)
	}

	return pretty, nil
}

// Example usage (for a separate CLI or dev tool, not called by the HTTP
// server):
//
//  func main() {
//      ctx := context.Background()
//      cfg := LoadConfig()
//      client, err := NewDicomWebClient(ctx, cfg)
//      if err != nil { log.Fatal(err) }
//
//      if err := client.RetrieveStudyToFile(ctx,
//          "1.3.6.1.4.1.11129.5.5.111396399857604",
//          "study.multipart"); err != nil {
//          log.Fatal(err)
//      }
//  }
