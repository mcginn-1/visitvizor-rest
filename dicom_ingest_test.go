package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

///////////////// Testing ///////////////////////////
//
//    go test -run Test_ManualPubSubDicomIngest -v
//

// testIngestMessage mirrors IngestMessage but is local to the test so we
// don't depend on implementation details.
type testIngestMessage struct {
	SessionID string `json:"session_id"`
	GCSPrefix string `json:"gcs_prefix"`
}

// testPubSubEnvelope mirrors the minimal Pub/Sub push envelope the handler
// expects.
type testPubSubEnvelope struct {
	Message struct {
		Data       string            `json:"data"`
		Attributes map[string]string `json:"attributes,omitempty"`
		MessageID  string            `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

// buildTestEnvelope builds the JSON body we would normally send from Pub/Sub
// when pushing to /internal/pubsub/dicom-ingest. It takes a plain session ID
// and GCS prefix and does the JSON + base64 wrapping for you.
func buildTestEnvelope(sessionID, gcsPrefix string) ([]byte, error) {
	// Inner message: IngestMessage JSON
	inner := testIngestMessage{
		SessionID: sessionID,
		GCSPrefix: gcsPrefix,
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return nil, err
	}

	// Base64-encode the inner JSON for the Pub/Sub data field
	encoded := base64.StdEncoding.EncodeToString(innerJSON)

	var env testPubSubEnvelope
	env.Message.Data = encoded
	env.Message.Attributes = map[string]string{}
	env.Message.MessageID = "local-test-1"
	env.Subscription = "projects/local-project/subscriptions/local-dicom-ingest"

	return json.Marshal(env)
}

// Test_ManualPubSubDicomIngest is a manual/integration-style test that
// constructs the Pub/Sub push envelope and POSTs it to a locally running
// VisitVizor REST server.
//
// Run the server first (go run ./...), then run:
//
//	go test -run Test_ManualPubSubDicomIngest -v
//
// Adjust the constants below to point at a real session and GCS prefix that
// already has uploaded DICOM files.
func Test_ManualPubSubDicomIngest(t *testing.T) {
	const (
		// Change these to a real session and prefix you want to ingest.
		//testSessionID = "REPLACE_WITH_SESSION_ID"
		//testGCSPrefix = "gs://vv-storage-vault/REPLACE_WITH_USER_ID/REPLACE_WITH_SESSION_ID/"
		//testSessionID = "SESS-CI2TKOLH"
		testSessionID = "SESS-GOVT5MBW"
		//testGCSPrefix = "gs://vv-storage-vault/SlM7A6UIqUUOdukWJrRJdQFh6eX2/SESS-CI2TKOLH"
		testGCSPrefix = "gs://vv-storage-vault/VaDLbMeAPFUiG0R7CMl35jE87e83/SESS-GOVT5MBW/"

		// URL for the locally running REST server.
		testURL = "http://localhost:8080/internal/pubsub/dicom-ingest"
	)

	body, err := buildTestEnvelope(testSessionID, testGCSPrefix)
	if err != nil {
		t.Fatalf("buildTestEnvelope: %v", err)
	}

	resp, err := http.Post(testURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", testURL, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	t.Logf("status=%d body=%s", resp.StatusCode, string(respBody))
	t.Logf("response: %v", respBody)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from ingest endpoint, got %d", resp.StatusCode)
	}
}
