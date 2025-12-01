package dicomweb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	healthcare "google.golang.org/api/healthcare/v1"
)

////////////////////////////////////////////////////////////
//
//
//       IF WANT TO USE IN SERVER
//
//import "visitvizor-rest/dicomweb" // module path matches your go.mod
//
//func someServerFunc(h *Handlers) error {
//	ctx := context.Background()
//	c, err := dicomweb.NewClient(
//		ctx,
//		h.Cfg.ProjectID,
//		h.Cfg.HealthcareLocation,
//		h.Cfg.HealthcareDatasetID,
//		h.Cfg.HealthcareStoreID,
//	)
//	if err != nil {
//		return err
//	}
//	// use c.RetrieveStudyToFile / c.DeleteStudy / c.StudyMetadataJSON
//	return nil
//}

type Client struct {
	projectID string
	location  string
	datasetID string
	storeID   string
	svc       *healthcare.Service
}

func NewClient(ctx context.Context, projectID, location, datasetID, storeID string) (*Client, error) {
	svc, err := healthcare.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("healthcare.NewService: %w", err)
	}
	return &Client{
		projectID: projectID,
		location:  location,
		datasetID: datasetID,
		storeID:   storeID,
		svc:       svc,
	}, nil
}

func (c *Client) dicomStoreParent() string {
	return fmt.Sprintf(
		"projects/%s/locations/%s/datasets/%s/dicomStores/%s",
		c.projectID, c.location, c.datasetID, c.storeID,
	)
}

func (c *Client) RetrieveStudyToFile(ctx context.Context, studyUID, outputFile string) error {
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

func (c *Client) DeleteStudy(ctx context.Context, studyUID string) error {
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

func (c *Client) StudyMetadataJSON(ctx context.Context, studyUID string) ([]byte, error) {
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

// RetrieveRenderedInstanceJPEG retrieves a rendered representation of a single
// DICOM instance as an HTTP response. The caller is responsible for closing
// resp.Body.
func (c *Client) RetrieveRenderedInstanceJPEG(
	ctx context.Context,
	studyUID, seriesUID, instanceUID string,
) (*http.Response, error) {
	if studyUID == "" || seriesUID == "" || instanceUID == "" {
		return nil, fmt.Errorf("studyUID, seriesUID, and instanceUID are required")
	}

	parent := c.dicomStoreParent()
	// Use the DICOMweb rendered endpoint for this instance. The Healthcare API
	// expects the DICOMweb path to include the trailing /rendered segment.
	dicomWebPath := fmt.Sprintf("studies/%s/series/%s/instances/%s/rendered", studyUID, seriesUID, instanceUID)

	instancesSvc := c.svc.Projects.Locations.Datasets.DicomStores.Studies.Series.Instances
	call := instancesSvc.RetrieveRendered(parent, dicomWebPath)
	// Request a rendered representation; allow the server to choose an image type.
	// Using a broad Accept avoids 406 Not Acceptable for servers that don't support
	// image/jpeg specifically.
	call.Header().Set("Accept", "image/jpeg, image/png, */*")

	resp, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("RetrieveRendered: %w", err)
	}

	return resp, nil
}

// RetrieveInstanceRaw retrieves a single DICOM instance (application/dicom or
// multipart) as returned by the DICOMweb Instances.RetrieveInstance endpoint.
// The caller is responsible for closing resp.Body.
func (c *Client) RetrieveInstanceRaw(
	ctx context.Context,
	studyUID, seriesUID, instanceUID string,
) (*http.Response, error) {
	if studyUID == "" || seriesUID == "" || instanceUID == "" {
		return nil, fmt.Errorf("studyUID, seriesUID, and instanceUID are required")
	}

	parent := c.dicomStoreParent()
	dicomWebPath := fmt.Sprintf("studies/%s/series/%s/instances/%s", studyUID, seriesUID, instanceUID)

	instancesSvc := c.svc.Projects.Locations.Datasets.DicomStores.Studies.Series.Instances
	resp, err := instancesSvc.RetrieveInstance(parent, dicomWebPath).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("RetrieveInstance: %w", err)
	}
	return resp, nil
}

// RetrieveFramesRaw retrieves one or more frames' pixel data for a given
// study/series/instance/frame list via the DICOMweb frames endpoint.
//
// It returns whatever the Healthcare API returns for this call, typically
//
//	Content-Type: multipart/related; type="application/octet-stream"; ...
//
// with parts of type application/octet-stream containing only PixelData bytes.
//
// The caller is responsible for closing resp.Body.
func (c *Client) RetrieveFramesRaw(
	ctx context.Context,
	studyUID, seriesUID, instanceUID, frameList, accept string,
) (*http.Response, error) {
	if studyUID == "" || seriesUID == "" || instanceUID == "" || frameList == "" {
		return nil, fmt.Errorf("studyUID, seriesUID, instanceUID, and frameList are required")
	}

	parent := c.dicomStoreParent()
	dicomWebPath := fmt.Sprintf(
		"studies/%s/series/%s/instances/%s/frames/%s",
		studyUID, seriesUID, instanceUID, frameList,
	)

	framesSvc := c.svc.Projects.
		Locations.
		Datasets.
		DicomStores.
		Studies.
		Series.
		Instances.
		Frames

	call := framesSvc.RetrieveFrames(parent, dicomWebPath)

	// Propagate the caller's Accept so OHIF's
	//   Accept: multipart/related; type=application/octet-stream; transfer-syntax=*
	// is honored by GCP.
	if accept != "" {
		call.Header().Set("Accept", accept)
	}

	resp, err := call.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("RetrieveFrames: %w", err)
	}
	return resp, nil
}
