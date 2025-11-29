package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"

	"github.com/suyashkumar/dicom"
	_ "github.com/suyashkumar/dicom/pkg/frame" // not used
	"github.com/suyashkumar/dicom/pkg/tag"

	healthcare "google.golang.org/api/healthcare/v1"
)
	//"github.com/suyashkumar/dicom/pkg/frame"


//	healthcare "google.golang.org/api/healthcare/v1"
//)

// IngestMessage is the payload we publish to Pub/Sub for DICOM ingest.
// It identifies the upload session and the GCS prefix to import from.
//
// Example JSON:
// {
//   "session_id": "SESS-ABC123",
//   "gcs_prefix": "gs://vv-storage-vault/<userId>/SESS-ABC123/"
// }
type IngestMessage struct {
	SessionID string `json:"session_id"`
	GCSPrefix string `json:"gcs_prefix"`
}

// pubsubPushEnvelope matches the structure Pub/Sub sends to push endpoints.
// See: https://cloud.google.com/pubsub/docs/push
// We only care about the base64-encoded data field.
type pubsubPushEnvelope struct {
	Message struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// DicomIngester wraps the Cloud Healthcare API client for DICOM operations.
type DicomIngester struct {
	cfg     Config
	service *healthcare.Service
}

// NewDicomIngester creates a Healthcare API client using ADC.
func NewDicomIngester(ctx context.Context, cfg Config) (*DicomIngester, error) {
	svc, err := healthcare.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("healthcare.NewService: %w", err)
	}
	return &DicomIngester{
		cfg:     cfg,
		service: svc,
	}, nil
}

func (di *DicomIngester) dicomStoreName() string {
	return fmt.Sprintf(
		"projects/%s/locations/%s/datasets/%s/dicomStores/%s",
		di.cfg.ProjectID,
		di.cfg.HealthcareLocation,
		di.cfg.HealthcareDatasetID,
		di.cfg.HealthcareStoreID,
	)
}

// ImportAllFromPrefix starts a DICOM import from a GCS prefix and returns the LRO name.
func (di *DicomIngester) ImportAllFromPrefix(ctx context.Context, gcsPrefix string) (string, error) {
	if !strings.HasPrefix(gcsPrefix, "gs://") {
		return "", fmt.Errorf("gcsPrefix must start with gs://, got %q", gcsPrefix)
	}

	// Healthcare import uses a URI that can include wildcards; a raw
	// folder-like prefix won't match anything unless an object has that
	// exact name. Turn the prefix into a glob that matches all objects
	// under that \"folder\".
	uri := strings.TrimRight(gcsPrefix, "/") + "/**"

	parent := di.dicomStoreName()
	storesService := di.service.Projects.Locations.Datasets.DicomStores

	req := &healthcare.ImportDicomDataRequest{
		GcsSource: &healthcare.GoogleCloudHealthcareV1DicomGcsSource{
			//Uri: gcsPrefix,
			Uri: uri,
		},
	}

	op, err := storesService.Import(parent, req).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("dicomStores.Import: %w", err)
	}

	log.Printf("Started DICOM import from %s → %s. Operation: %s", gcsPrefix, parent, op.Name)
	return op.Name, nil
}

// WaitForOperation polls the given long-running operation until completion or context cancel.
func (di *DicomIngester) WaitForOperation(ctx context.Context, opName string) error {
	ops := di.service.Projects.Locations.Datasets.Operations

	log.Printf("WaitForOperation: starting poll for %s", opName)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("WaitForOperation: context cancelled for %s: %v", opName, ctx.Err())
			return ctx.Err()
		case <-ticker.C:
			op, err := ops.Get(opName).Context(ctx).Do()
			if err != nil {
				log.Printf("WaitForOperation: operations.Get(%s) error: %v", opName, err)
				return fmt.Errorf("operations.Get(%s): %w", opName, err)
			}

			log.Printf("WaitForOperation: op=%s done=%v", op.Name, op.Done)

			if op.Done {
				if op.Error != nil && op.Error.Message != "" {
					log.Printf("WaitForOperation: op %s failed: %s", opName, op.Error.Message)
					return fmt.Errorf("dicom import failed: %s", op.Error.Message)
				}

				log.Printf("WaitForOperation: op %s completed successfully", opName)
				return nil
			}
		}
	}
}


//// WaitForOperation polls the given long-running operation until completion or context cancel.
//func (di *DicomIngester) WaitForOperation(ctx context.Context, opName string) error {
//	ops := di.service.Projects.Locations.Datasets.Operations
//
//	log.Printf("WaitForOperation: starting poll for %s", opName)
//
//	ticker := time.NewTicker(5 * time.Second)
//	defer ticker.Stop()
//
//	for {
//		select {
//		case <-ctx.Done():
//			log.Printf("WaitForOperation: context cancelled for %s: %v", opName, ctx.Err())
//			return ctx.Err()
//		case <-ticker.C:
//			op, err := ops.Get(opName).Context(ctx).Do()
//			if err != nil {
//				log.Printf("WaitForOperation: operations.Get(%s) error: %v", opName, err)
//				return fmt.Errorf("operations.Get(%s): %w", opName, err)
//			}
//
//			// Debug: dump high-level metadata
//			log.Printf("WaitForOperation: op=%s done=%v", op.Name, op.Done)
//
//			if op.Done {
//				if op.Error != nil && op.Error.Message != "" {
//					log.Printf("WaitForOperation: op %s failed: %s", opName, op.Error.Message)
//					return fmt.Errorf("dicom import failed: %s", op.Error.Message)
//				}
//
//				log.Printf("WaitForOperation: op %s completed successfully", opName)
//				return nil
//			}
//		}
//	}
//}
//// WaitForOperation polls the given long-running operation until completion or context cancel.
//func (di *DicomIngester) WaitForOperation(ctx context.Context, opName string) error {
//	ops := di.service.Projects.Locations.Datasets.Operations
//
//	for {
//		op, err := ops.Get(opName).Context(ctx).Do()
//		if err != nil {
//			return fmt.Errorf("operations.Get(%s): %w", opName, err)
//		}
//
//		if op.Done {
//			if op.Error != nil && op.Error.Message != "" {
//				return fmt.Errorf("dicom import failed: %s", op.Error.Message)
//			}
//			// Success.
//			return nil
//		}
//
//		select {
//		case <-ctx.Done():
//			return ctx.Err()
//		case <-time.After(5 * time.Second):
//		}
//	}
//}

// dicomInstanceInfo captures the minimal DICOM header info we care about
// for grouping logical studies.
type dicomInstanceInfo struct {
	StudyInstanceUID  string
	SeriesInstanceUID string
	SOPInstanceUID    string
	Modality          string
	StudyDate         string
	StudyDescription  string
}

// getStringByTag extracts the first string value for the given tag from
// the dataset, using dicom.MustGetStrings on the element's value so that
// we store clean values like "CT" or "1.2.840...." instead of the verbose
// Element.String() representation.
func getStringByTag(ds *dicom.Dataset, t tag.Tag) string {
	if ds == nil {
		return ""
	}
	el, err := ds.FindElementByTag(t)
	if err != nil || el == nil {
		return ""
	}
	vals := dicom.MustGetStrings(el.Value)
	if len(vals) == 0 {
		return ""
	}
	return strings.TrimSpace(vals[0])
}

//// helper to pull a string out of a Dataset by tag.
//func getStringByTag(ds *dicom.Dataset, t tag.Tag) string {
//	if ds == nil {
//		return ""
//	}
//	el, err := ds.FindElementByTag(t)
//	if err != nil || el == nil {
//		return ""
//	}
//	// Element has String() and that's enough for StudyInstanceUID, StudyDate, etc.
//	return strings.TrimSpace(el.String())
//}

//// helper to pull a string out of a DataSet by tag.
//func getStringByTag(ds *dicom.Dataset, t tag.Tag) string {
//	if ds == nil {
//		return ""
//	}
//	el, err := ds.FindElementByTag(t)
//	if err != nil || el == nil {
//		return ""
//	}
//	// For string values, extract via GetString if available
//if val, err := el.Value.GetString(); err == nil {
//		return strings.TrimSpace(val)
//	}
//	// Fallback to String() representation
//	return strings.TrimSpace(el.String())
//}

// looksLikeDicomObjectName is a conservative filter to skip obviously-non-DICOM files.
func looksLikeDicomObjectName(name string) bool {
	name = strings.ToLower(name)
	if strings.HasSuffix(name, "/") {
		return false
	}
	if strings.HasSuffix(name, ".dcm") || strings.HasSuffix(name, ".dicom") {
		return true
	}
	// Many PACS exports omit extensions; treat them as candidates.
	if !strings.Contains(name, ".") {
		return true
	}
	// Skip common non-DICOM extensions.
	if strings.HasSuffix(name, ".txt") || strings.HasSuffix(name, ".pdf") ||
		strings.HasSuffix(name, ".csv") || strings.HasSuffix(name, ".json") ||
		strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".zip") {
		return false
	}
	return false
}

//////////////////////////////////////////////////////////////////
//
//   SCAN DICOM INSTANCES in GCS bucket that were just uploaded
//
//   Build DICOM Instance Info
//
//     Returns a map of [string /* always studyId */][]dicomInstanceInfo
//
//			type dicomInstanceInfo struct {
//				StudyInstanceUID  string
//				SeriesInstanceUID string
//				SOPInstanceUID    string
//				Modality          string
//				StudyDate         string
//				StudyDescription  string
//			}
//
//
// collectDicomInstances scans all objects under the given GCS prefix and
// returns a map keyed by StudyInstanceUID with the per-instance header info.
func (h *Handlers) collectDicomInstances(ctx context.Context, gcsPrefix string) (map[string][]dicomInstanceInfo, error) {
	if !strings.HasPrefix(gcsPrefix, "gs://") {
		return nil, fmt.Errorf("gcsPrefix must start with gs://, got %q", gcsPrefix)
	}

	// Split gs://bucket/prefix...
	trimmed := strings.TrimPrefix(gcsPrefix, "gs://")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid gcsPrefix %q", gcsPrefix)
	}
	bucketName := parts[0]
	objectPrefix := parts[1]

	if h.Storage == nil {
		return nil, fmt.Errorf("storage client not initialized on Handlers")
	}

	studies := make(map[string][]dicomInstanceInfo)

	it := h.Storage.Bucket(bucketName).Objects(ctx, &storage.Query{Prefix: objectPrefix})

	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate GCS objects under %s: %w", gcsPrefix, err)
		}

		if !looksLikeDicomObjectName(attrs.Name) {
			continue
		}

		rc, err := h.Storage.Bucket(bucketName).Object(attrs.Name).NewReader(ctx)
		if err != nil {
			// handle
		}
		defer rc.Close()

		ds, err := dicom.Parse(rc, attrs.Size, nil, dicom.SkipPixelData())
		if err != nil {
			log.Printf("collectDicomInstances: Parse(%s): %v", attrs.Name, err)
			continue
		}

		// ds is a dicom.Dataset value; pass pointer into your helper:
		studyUID := getStringByTag(&ds, tag.StudyInstanceUID)
		seriesUID := getStringByTag(&ds, tag.SeriesInstanceUID)
		sopUID   := getStringByTag(&ds, tag.SOPInstanceUID)
		modality := getStringByTag(&ds, tag.Modality)
		studyDate := getStringByTag(&ds, tag.StudyDate)
		desc      := getStringByTag(&ds, tag.StudyDescription)

		//// Open the object and parse just the header (drop pixel data).
		//rc, err := h.Storage.Bucket(bucketName).Object(attrs.Name).NewReader(ctx)
		//if err != nil {
		//	log.Printf("collectDicomInstances: open %s: %v", attrs.Name, err)
		//	continue
		//}
		//
		//// Parse with DropPixelData to only read headers
		//ds, err := dicom.ParseFile(rc, rc.Size(), dicom.SkipPixelData())
		//_ = rc.Close()
		//if err != nil {
		//	log.Printf("collectDicomInstances: ParseFile(%s): %v", attrs.Name, err)
		//	continue
		//}
		//
		//studyUID := getStringByTag(&ds, tag.StudyInstanceUID)
		//seriesUID := getStringByTag(&ds, tag.SeriesInstanceUID)
		//sopUID := getStringByTag(&ds, tag.SOPInstanceUID)
		//modality := getStringByTag(&ds, tag.Modality)
		//studyDate := getStringByTag(&ds, tag.StudyDate)
		//desc := getStringByTag(&ds, tag.StudyDescription)

		if studyUID == "" || sopUID == "" {
			// Not a valid DICOM instance for our purposes.
			continue
		}

		info := dicomInstanceInfo{
			StudyInstanceUID:  studyUID,
			SeriesInstanceUID: seriesUID,
			SOPInstanceUID:    sopUID,
			Modality:          modality,
			StudyDate:         studyDate,
			StudyDescription:  desc,
		}

		studies[studyUID] = append(studies[studyUID], info)
	}

	return studies, nil
}

////////////////////////////////////////////////////////////////////////
//
//     Takes a flat header scan result from all the files 
//
//       a map of [string /* always studyId */][]dicomInstanceInfo
//
//       and rolls up the data by StudyID, with multiple Series and Modalities
//
//       study := &ImagingStudy{
//			StudyID:            studyID,
//			UserID:             sess.UserID,
//			SessionID:          sess.SessionID,
//			StudyInstanceUID:   studyUID,
//			SeriesInstanceUIDs: seriesUIDs,
//			ModalitiesInStudy:  modalities,
//			StudyDate:          studyDate,
//			StudyDescription:   studyDescription,
//			NumInstances:       len(instances),
//			GCSPrefix:          gcsPrefix,
//			DicomStorePath:     dicomStorePath,
//			CreatedAt:          time.Now().UTC(),
//		}
//
//
// createImagingStudiesFromInstances groups instances by StudyInstanceUID and
// writes one ImagingStudy document per study.
func (h *Handlers) createImagingStudiesFromInstances(ctx context.Context, sess *UploadSession, gcsPrefix string, studies map[string][]dicomInstanceInfo) error {
	if len(studies) == 0 {
		return fmt.Errorf("no DICOM studies detected under %s", gcsPrefix)
	}

	dicomStorePath := fmt.Sprintf(
		"projects/%s/locations/%s/datasets/%s/dicomStores/%s",
		h.Cfg.ProjectID,
		h.Cfg.HealthcareLocation,
		h.Cfg.HealthcareDatasetID,
		h.Cfg.HealthcareStoreID,
	)

	for studyUID, instances := range studies {
		seriesSet := make(map[string]struct{})
		modalitySet := make(map[string]struct{})

		var studyDate, studyDescription string

		for _, inst := range instances {
			if inst.SeriesInstanceUID != "" {
				seriesSet[inst.SeriesInstanceUID] = struct{}{}
			}
			if inst.Modality != "" {
				modalitySet[inst.Modality] = struct{}{}
			}
			if studyDescription == "" && inst.StudyDescription != "" {
				studyDescription = inst.StudyDescription
			}
			if studyDate == "" && inst.StudyDate != "" {
				studyDate = inst.StudyDate
			}
		}

		seriesUIDs := make([]string, 0, len(seriesSet))
		for uid := range seriesSet {
			seriesUIDs = append(seriesUIDs, uid)
		}

		modalities := make([]string, 0, len(modalitySet))
		for m := range modalitySet {
			modalities = append(modalities, m)
		}

		studyID, err := randomTokenID("STUDY", 10)
		if err != nil {
			return fmt.Errorf("randomTokenID for study: %w", err)
		}

		study := &ImagingStudy{
			StudyID:            studyID,
			UserID:             sess.UserID,
			SessionID:          sess.SessionID,
			StudyInstanceUID:   studyUID,
			SeriesInstanceUIDs: seriesUIDs,
			ModalitiesInStudy:  modalities,
			StudyDate:          studyDate,
			StudyDescription:   studyDescription,
			NumInstances:       len(instances),
			GCSPrefix:          gcsPrefix,
			DicomStorePath:     dicomStorePath,
			CreatedAt:          time.Now().UTC(),
		}

		if err := h.DB.CreateImagingStudy(ctx, study); err != nil {
			return err
		}
	}

	return nil
}

// handleIngestMessage performs the full ingest flow for a single message:
// - validate and load the UploadSession
// - mark it as importing
// - start DICOM import from GCS prefix
// - wait for completion
// - group instances into ImagingStudy docs
// - set status to ready or error accordingly.
//
//
//     THIS IS THE MAIN INGESTION POINT INTO GOOGLE DICOM STORE
//
//
func (h *Handlers) handleIngestMessage(ctx context.Context, msg IngestMessage) error {
	if strings.TrimSpace(msg.SessionID) == "" || strings.TrimSpace(msg.GCSPrefix) == "" {
		return fmt.Errorf("missing session_id or gcs_prefix in message")
	}

	// Load the upload session.
	sess, err := h.DB.GetUploadSession(ctx, msg.SessionID)
	if err != nil {
		return fmt.Errorf("GetUploadSession(%s): %w", msg.SessionID, err)
	}
	if sess == nil {
		return fmt.Errorf("upload session %s not found", msg.SessionID)
	}

	// Mark as importing and store GCS prefix (and clear any previous error).
	if err := h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
		"status":                  "importing",
		"error_message":           "",
		"gcs_prefix":              msg.GCSPrefix,
		"dicom_import_operation":  "",
	}); err != nil {
		return fmt.Errorf("UpdateUploadSessionStatus(importing): %w", err)
	}

	// Create a DICOM ingester for this request.
	ingester, err := NewDicomIngester(ctx, h.Cfg)
	if err != nil {
		// Mark error and return.
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": fmt.Sprintf("NewDicomIngester: %v", err),
		})
		return err
	}

	opName, err := ingester.ImportAllFromPrefix(ctx, msg.GCSPrefix)
	if err != nil {
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": fmt.Sprintf("ImportAllFromPrefix: %v", err),
		})
		return err
	}
	log.Printf("handleIngestMessage: started import op %s for session %s", opName, msg.SessionID)

	// Persist operation name for debugging / later re-checks.
	if err := h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
		"dicom_import_operation": opName,
	}); err != nil {
		return fmt.Errorf("UpdateUploadSessionStatus(set opName): %w", err)
	}

	// Block until import is done (for now). This assumes imports are reasonably small.
	// Cycles around while polling until it throws err; 'done' =  err
	if err := ingester.WaitForOperation(ctx, opName); err != nil {
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": err.Error(),
		})
		return err
	}

	// At this point import succeeded; scan headers under the same prefix and
	// create one ImagingStudy per StudyInstanceUID.
	//     studyInstances = a map of [string /* always studyId */][]dicomInstanceInfo
	//
	//			type dicomInstanceInfo struct {
	//				StudyInstanceUID  string
	//				SeriesInstanceUID string
	//				SOPInstanceUID    string
	//				Modality          string
	//				StudyDate         string
	//				StudyDescription  string
	//			}
	//
	//
	studyInstances, err := h.collectDicomInstances(ctx, msg.GCSPrefix)
	if err != nil {
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": fmt.Sprintf("collectDicomInstances: %v", err),
		})
		return err
	}
	
	////////////////////////////////////////////////////////////////////////
	//
	//     Takes a flat header scan result from all the files 
	//
	//       a map of [string /* always studyId */][]dicomInstanceInfo
	//
	//       and rolls up the data by StudyID, with multiple Series and Modalities
	//
	//        (This is the data that feeds our Image Viewer summaries for a single study)
	//               /imaging/studies or detail /imaging/studies/{studyID}
	//
	//       study := &ImagingStudy{
	//			StudyID:            studyID,
	//			UserID:             sess.UserID,
	//			SessionID:          sess.SessionID,
	//			StudyInstanceUID:   studyUID,
	//			SeriesInstanceUIDs: seriesUIDs,
	//			ModalitiesInStudy:  modalities,
	//			StudyDate:          studyDate,
	//			StudyDescription:   studyDescription,
	//			NumInstances:       len(instances),
	//			GCSPrefix:          gcsPrefix,
	//			DicomStorePath:     dicomStorePath,
	//			CreatedAt:          time.Now().UTC(),
	//		}
	//
	//
	if err := h.createImagingStudiesFromInstances(ctx, sess, msg.GCSPrefix, studyInstances); err != nil {
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": fmt.Sprintf("createImagingStudies: %v", err),
		})
		return err
	}

	// Success: mark session as ready.
	if err := h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
		"status": "ready",
	}); err != nil {
		return fmt.Errorf("UpdateUploadSessionStatus(ready): %w", err)
	}

	return nil
}

// PubSubDicomIngestHandler is the HTTP endpoint Pub/Sub will call
// to trigger DICOM ingestion for a given upload session.
func (h *Handlers) PubSubDicomIngestHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	var env pubsubPushEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		log.Printf("PubSubDicomIngest: invalid envelope: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if env.Message.Data == "" {
		log.Printf("PubSubDicomIngest: empty message data")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(env.Message.Data)
	if err != nil {
		log.Printf("PubSubDicomIngest: base64 decode error: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var msg IngestMessage
	if err := json.Unmarshal(decoded, &msg); err != nil {
		log.Printf("PubSubDicomIngest: invalid message json: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("PubSubDicomIngest: processing session_id=%s gcs_prefix=%s", msg.SessionID, msg.GCSPrefix)

	if err := h.handleIngestMessage(ctx, msg); err != nil {
		log.Printf("PubSubDicomIngest: handleIngestMessage error: %v", err)
		// Non-2xx tells Pub/Sub to retry the message.
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
