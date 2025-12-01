package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// getenvDefault returns env var value or a default if empty.
func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// TestCompareImagingAndDicomweb fetches the same instance through the legacy
// /api/imaging viewer endpoint and the new /api/dicomweb endpoint so you can
// compare the raw responses on disk.
//
// It expects the backend to be running (e.g., on http://localhost:8080) and
// uses the following environment variables:
//
//	VISITVIZOR_BASE_URL   - optional, default "http://localhost:8080"
//	VISITVIZOR_AUTH_BEARER - Authorization bearer token (Firebase or dev)
//	VISITVIZOR_USER_ID     - user id to send in X-User-Id (for dev flows)
//	VISITVIZOR_STUDY_ID    - internal ImagingStudy StudyID (for /api/imaging)
//	VISITVIZOR_STUDY_UID   - DICOM StudyInstanceUID
//	VISITVIZOR_SERIES_UID  - DICOM SeriesInstanceUID
//	VISITVIZOR_SOP_UID     - DICOM SOPInstanceUID
//
// It writes two files under testdata/:
//
//	testdata/imaging_frame1.bin  - body from /api/imaging/.../frames/1
//	testdata/dicomweb_frame1.bin - body from /api/dicomweb/.../frames/1
func TestCompareImagingAndDicomweb(t *testing.T) {
	baseURL := getenvDefault("VISITVIZOR_BASE_URL", "http://localhost:8080")
	bearer := "123456"                                                      //os.Getenv("VISITVIZOR_AUTH_BEARER")
	userID := "SlM7A6UIqUUOdukWJrRJdQFh6eX2"                                //os.Getenv("VISITVIZOR_USER_ID")
	studyID := "STUDY-P6527WJ5"                                             //os.Getenv("VISITVIZOR_STUDY_ID")
	studyUID := "1.2.840.113619.2.428.3.319444734.713.1759316145.343"       //os.Getenv("VISITVIZOR_STUDY_UID")
	seriesUID := "1.2.840.113619.2.5.319444734.2019162223.251001133911.601" //os.Getenv("VISITVIZOR_SERIES_UID")
	sopUID := "1.2.840.113619.2.5.20359361.11231.1759320481.71"             //os.Getenv("VISITVIZOR_SOP_UID")

	// http://localhost:8080/api/dicomweb/studies/1.2.840.113619.2.428.3.319444734.713.1759316145.343/series/1.2.840.113619.2.5.319444734.2019162223.251001133911.601/instances/1.2.840.113619.2.5.20359361.11231.1759320481.71/frames/1

	//if bearer == "" || userID == "" || studyID == "" || studyUID == "" || seriesUID == "" || sopUID == "" {
	//    t.Skip("set VISITVIZOR_AUTH_BEARER, VISITVIZOR_USER_ID, VISITVIZOR_STUDY_ID, VISITVIZOR_STUDY_UID, VISITVIZOR_SERIES_UID, VISITVIZOR_SOP_UID to run this test")
	//}

	client := &http.Client{Timeout: 30 * time.Second}

	imagingURL := fmt.Sprintf("%s/api/imaging/studies/%s/dicom/series/%s/instances/%s/frames/1", baseURL, studyID, seriesUID, sopUID)
	dicomwebURL := fmt.Sprintf("%s/api/dicomweb/studies/%s/series/%s/instances/%s/frames/1", baseURL, studyUID, seriesUID, sopUID)

	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}

	doGet := func(url, outPath string) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("NewRequest %s: %v", url, err)
		}
		// Use the same headers you normally send from the frontend.
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("X-User-Id", userID)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("GET %s: status %d %s, body: %s", url, resp.StatusCode, resp.Status, string(body))
		}

		f, err := os.Create(outPath)
		if err != nil {
			t.Fatalf("create %s: %v", outPath, err)
		}
		defer f.Close()

		ct := resp.Header.Get("Content-Type")
		t.Logf("Content-Type for %s: %s", url, ct)

		if strings.Contains(ct, "application/dicom") {
			outPath += ".dcm"
		} else if strings.Contains(ct, "image/jpeg") {
			outPath += ".jpg"
		}

		if _, err := io.Copy(f, resp.Body); err != nil {
			t.Fatalf("write %s: %v", outPath, err)
		}
	}

	t.Logf("Fetching imaging endpoint: %s", imagingURL)
	doGet(imagingURL, "testdata/imaging_frame1.bin")

	t.Logf("Fetching dicomweb endpoint: %s", dicomwebURL)
	doGet(dicomwebURL, "testdata/dicomweb_frame1.bin")
}
