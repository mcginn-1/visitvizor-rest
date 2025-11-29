package main
//
//import (
//	"context"
//	"fmt"
//	"io"
//	"log"
//	"strings"
//
//	healthcare "google.golang.org/api/healthcare/v1"
//)
//
//type DicomIngester struct {
//	cfg     Config
//	service *healthcare.Service
//}
//
//func NewDicomIngester(ctx context.Context, cfg Config) (*DicomIngester, error) {
//	svc, err := healthcare.NewService(ctx)
//	if err != nil {
//		return nil, fmt.Errorf("healthcare.NewService: %w", err)
//	}
//	return &DicomIngester{
//		cfg:     cfg,
//		service: svc,
//	}, nil
//}
//
//func (di *DicomIngester) dicomStoreName() string {
//	return fmt.Sprintf(
//		"projects/%s/locations/%s/datasets/%s/dicomStores/%s",
//		di.cfg.ProjectID,
//		di.cfg.HealthcareLocation,
//		di.cfg.HealthcareDatasetID, // "vv-dataset-1"
//		di.cfg.HealthcareStoreID,   // "vv-dicom"
//	)
//}
//
//// ImportAllFromPrefix starts a DICOM import from a GCS prefix, returns the operation name.
//func (di *DicomIngester) ImportAllFromPrefix(ctx context.Context, w io.Writer, gcsPrefix string) (string, error) {
//	if !strings.HasPrefix(gcsPrefix, "gs://") {
//		return "", fmt.Errorf("gcsPrefix must start with gs://, got %q", gcsPrefix)
//	}
//
//	parent := di.dicomStoreName()
//
//	storesService := di.service.Projects.Locations.Datasets.DicomStores
//	req := &healthcare.ImportDicomDataRequest{
//		GcsSource: &healthcare.GoogleCloudHealthcareV1DicomGcsSource{
//			Uri: gcsPrefix,
//		},
//	}
//
//	op, err := storesService.Import(parent, req).Context(ctx).Do()
//	if err != nil {
//		return "", fmt.Errorf("dicomStores.Import: %w", err)
//	}
//
//	if w != nil {
//		fmt.Fprintf(w, "Started DICOM import from %s → %s. Operation: %s\n", gcsPrefix, parent, op.Name)
//	} else {
//		log.Printf("Started DICOM import from %s → %s. Operation: %s", gcsPrefix, parent, op.Name)
//	}
//
//	return op.Name, nil
//}
