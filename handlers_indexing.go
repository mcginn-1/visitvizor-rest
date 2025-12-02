package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// ////////////////////////////////////////////////////
//
//	ENDPOINT: /api/imaging/longitudinal/index
//
//	Kick off Indexing on a set of studies
//
// LongitudinalIndexHandler implements POST /api/imaging/longitudinal/index.
// Body: { "studyIds": ["STUDY-ID-1", ...] }
func (h *Handlers) LongitudinalIndexHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	callerID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("LongitudinalIndexHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	var body struct {
		StudyIDs []string `json:"studyIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}
	if len(body.StudyIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "studyIds required"})
		return
	}

	for _, studyID := range body.StudyIDs {
		studyID = strings.TrimSpace(studyID)
		if studyID == "" {
			continue
		}

		study, err := h.DB.GetImagingStudy(ctx, studyID)
		if err != nil {
			log.Printf("LongitudinalIndexHandler GetImagingStudy(%s) error: %v", studyID, err)
			continue
		}
		if study == nil {
			log.Printf("LongitudinalIndexHandler study %s not found", studyID)
			continue
		}

		// ACCESS CHECK v1: caller must be the patient/user who owns the study.
		// Later you can relax this to allow doctor access via an approval list.
		if study.UserID != callerID {
			log.Printf("LongitudinalIndexHandler: caller %s not owner of study %s", callerID, studyID)
			continue
		}

		// Mark status = indexing
		_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
			StudyID:       study.StudyID,
			PatientUserID: study.UserID,
			Status:        "indexing",
			LastError:     "",
		})

		slices, err := h.buildIndexedSlicesForStudy(ctx, study)
		if err != nil {
			log.Printf("buildIndexedSlicesForStudy(%s) error: %v", studyID, err)
			_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
				StudyID:       study.StudyID,
				PatientUserID: study.UserID,
				Status:        "error",
				LastError:     err.Error(),
			})
			continue
		}

		if err := h.DB.SaveIndexedSlicesForStudy(ctx, study.StudyID, slices); err != nil {
			log.Printf("SaveIndexedSlicesForStudy(%s) error: %v", studyID, err)
			_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
				StudyID:       study.StudyID,
				PatientUserID: study.UserID,
				Status:        "error",
				LastError:     err.Error(),
			})
			continue
		}

		_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
			StudyID:       study.StudyID,
			PatientUserID: study.UserID,
			Status:        "indexed",
			LastError:     "",
		})
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{"ok": true})
}

// LongitudinalIndexHandler implements POST /api/imaging/longitudinal/index.
// Body: { "studyIds": ["STUDY-...","STUDY-..."] }
//func (h *Handlers) LongitudinalIndexHandler(w http.ResponseWriter, r *http.Request) {
//	if r.Method != http.MethodPost {
//		w.WriteHeader(http.StatusMethodNotAllowed)
//		return
//	}
//	ctx := r.Context()
//
//	// Caller identity (patient or doctor). We'll relax this later
//	// via a helper that checks doctor approvals; for now it's just UID.
//	callerID, err := h.GetUserIDFromRequest(ctx, r)
//	if err != nil {
//		log.Printf("LongitudinalIndexHandler getUserIDFromRequest error: %v", err)
//		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
//		return
//	}
//
//	var body struct {
//		StudyIDs []string `json:"studyIds"`
//	}
//	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
//		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
//		return
//	}
//	if len(body.StudyIDs) == 0 {
//		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "studyIds required"})
//		return
//	}
//
//	// For v1, just do indexing synchronously per study.
//	// Later you can push to a background worker.
//	for _, studyID := range body.StudyIDs {
//		studyID = strings.TrimSpace(studyID)
//		if studyID == "" {
//			continue
//		}
//
//		study, err := h.DB.GetImagingStudy(ctx, studyID)
//		if err != nil {
//			log.Printf("LongitudinalIndexHandler GetImagingStudy(%s) error: %v", studyID, err)
//			continue
//		}
//		if study == nil {
//			log.Printf("LongitudinalIndexHandler study %s not found", studyID)
//			continue
//		}
//
//		// ACCESS CHECK (v1): caller must be the patient.
//		// Later: factor this out to a helper that also checks doctor approvals.
//		if study.UserID != callerID {
//			log.Printf("LongitudinalIndexHandler: caller %s not owner of study %s", callerID, studyID)
//			continue
//		}
//
//		// Mark status = indexing
//		_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
//			StudyID:       study.StudyID,
//			PatientUserID: study.UserID,
//			Status:        "indexing",
//			LastError:     "",
//		})
//
//		slices, err := h.buildIndexedSlicesForStudy(ctx, study)
//		if err != nil {
//			log.Printf("buildIndexedSlicesForStudy(%s) error: %v", studyID, err)
//			_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
//				StudyID:       study.StudyID,
//				PatientUserID: study.UserID,
//				Status:        "error",
//				LastError:     err.Error(),
//			})
//			continue
//		}
//
//		if err := h.DB.SaveIndexedSlicesForStudy(ctx, study.StudyID, slices); err != nil {
//			log.Printf("SaveIndexedSlicesForStudy(%s) error: %v", studyID, err)
//			_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
//				StudyID:       study.StudyID,
//				PatientUserID: study.UserID,
//				Status:        "error",
//				LastError:     err.Error(),
//			})
//			continue
//		}
//
//		_ = h.DB.SetLongitudinalIndexStatus(ctx, &LongitudinalIndexStatus{
//			StudyID:       study.StudyID,
//			PatientUserID: study.UserID,
//			Status:        "indexed",
//			LastError:     "",
//		})
//	}
//
//	writeJSON(w, http.StatusAccepted, map[string]interface{}{
//		"ok": true,
//	})
//}

// ////////////////////////////////////////////////////
//
//	ENDPOINT: /api/imaging/longitudinal/index-status?studyIds=ID1,ID2,...
//
//	Get STATUS of indexing for a set of studies
//
// LongitudinalIndexStatusHandler implements
// GET /api/imaging/longitudinal/index-status?studyIds=ID1,ID2,...
func (h *Handlers) LongitudinalIndexStatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	callerID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("LongitudinalIndexStatusHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	raw := r.URL.Query().Get("studyIds")
	if strings.TrimSpace(raw) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "studyIds required"})
		return
	}

	ids := strings.Split(raw, ",")
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		study, err := h.DB.GetImagingStudy(ctx, id)
		if err != nil || study == nil {
			continue
		}
		if study.UserID != callerID {
			continue
		}
		filtered = append(filtered, id)
	}

	statuses, err := h.DB.GetLongitudinalIndexStatuses(ctx, filtered)
	if err != nil {
		log.Printf("LongitudinalIndexStatusHandler GetLongitudinalIndexStatuses error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "server_error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"statuses": statuses,
	})
}

// LongitudinalResolvePointHandler implements
// POST /api/imaging/longitudinal/resolve-point
// Body:
//
//	{
//	  "patientId": "...",            // optional for v1
//	  "frameOfReferenceUid": "1.2.3",
//	  "x": 12.3,
//	  "y": -45.6,
//	  "z": 78.9,
//	  "studyIds": ["STUDY-ID-1", ...]
//	}
//
// Response: array of matches, one per study (when available), each with
//
//	studyId, studyInstanceUid, seriesInstanceUid, sopInstanceUid,
//	instanceNumber, row, col.
func (h *Handlers) LongitudinalResolvePointHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	callerID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("LongitudinalResolvePointHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
		return
	}

	var body struct {
		PatientID           string   `json:"patientId"`
		FrameOfReferenceUID string   `json:"frameOfReferenceUid"`
		X                   float64  `json:"x"`
		Y                   float64  `json:"y"`
		Z                   float64  `json:"z"`
		StudyIDs            []string `json:"studyIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "invalid_json"})
		return
	}

	if len(body.StudyIDs) == 0 || strings.TrimSpace(body.FrameOfReferenceUID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "frameOfReferenceUid and studyIds required"})
		return
	}

	// For v1, we only allow access to studies owned by the caller.
	validStudyIDs := make([]string, 0, len(body.StudyIDs))
	studyMeta := make(map[string]*ImagingStudy)
	for _, id := range body.StudyIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		study, err := h.DB.GetImagingStudy(ctx, id)
		if err != nil || study == nil {
			continue
		}
		if study.UserID != callerID {
			continue
		}
		validStudyIDs = append(validStudyIDs, id)
		studyMeta[id] = study
	}

	if len(validStudyIDs) == 0 {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	type match struct {
		StudyID           string  `json:"studyId"`
		StudyInstanceUID  string  `json:"studyInstanceUid"`
		SeriesInstanceUID string  `json:"seriesInstanceUid"`
		SOPInstanceUID    string  `json:"sopInstanceUid"`
		InstanceNumber    int     `json:"instanceNumber"`
		Row               float64 `json:"row"`
		Col               float64 `json:"col"`
	}

	results := make([]match, 0, len(validStudyIDs))

	for _, studyID := range validStudyIDs {
		study := studyMeta[studyID]
		if study == nil {
			continue
		}

		slices, err := h.DB.ListIndexedSlicesForStudyAndFoR(ctx, studyID, body.FrameOfReferenceUID)
		if err != nil {
			log.Printf("LongitudinalResolvePointHandler ListIndexedSlicesForStudyAndFoR(%s) error: %v", studyID, err)
			continue
		}
		if len(slices) == 0 {
			continue
		}

		// Find slice with minimal distance to plane.
		var best *IndexedSlice
		bestDist := 0.0
		for i, s := range slices {
			d := distanceToSlicePlane(s, body.X, body.Y, body.Z)
			if i == 0 || d < bestDist {
				bestDist = d
				best = s
			}
		}
		if best == nil {
			continue
		}

		row, col := projectPointToSlice(best, body.X, body.Y, body.Z)

		results = append(results, match{
			StudyID:           study.StudyID,
			StudyInstanceUID:  study.StudyInstanceUID,
			SeriesInstanceUID: best.SeriesInstanceUID,
			SOPInstanceUID:    best.SOPInstanceUID,
			InstanceNumber:    best.InstanceNumber,
			Row:               row,
			Col:               col,
		})
	}

	writeJSON(w, http.StatusOK, results)
}

// LongitudinalIndexStatusHandler implements
// GET /api/imaging/longitudinal/index-status?studyIds=ID1,ID2,...
//func (h *Handlers) LongitudinalIndexStatusHandler(w http.ResponseWriter, r *http.Request) {
//	if r.Method != http.MethodGet {
//		w.WriteHeader(http.StatusMethodNotAllowed)
//		return
//	}
//	ctx := r.Context()
//
//	callerID, err := h.GetUserIDFromRequest(ctx, r)
//	if err != nil {
//		log.Printf("LongitudinalIndexStatusHandler getUserIDFromRequest error: %v", err)
//		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "unauthorized"})
//		return
//	}
//
//	raw := r.URL.Query().Get("studyIds")
//	if strings.TrimSpace(raw) == "" {
//		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"error": "studyIds required"})
//		return
//	}
//	ids := strings.Split(raw, ",")
//	for i := range ids {
//		ids[i] = strings.TrimSpace(ids[i])
//	}
//
//	// Optional: filter to only studies caller can access (patient or doctor).
//	// For now, lightly enforce: drop any study whose ImagingStudy.UserID != callerID.
//	filteredIDs := make([]string, 0, len(ids))
//	for _, id := range ids {
//		if id == "" {
//			continue
//		}
//		study, err := h.DB.GetImagingStudy(ctx, id)
//		if err != nil || study == nil {
//			continue
//		}
//		if study.UserID != callerID {
//			continue
//		}
//		filteredIDs = append(filteredIDs, id)
//	}
//
//	statuses, err := h.DB.GetLongitudinalIndexStatuses(ctx, filteredIDs)
//	if err != nil {
//		log.Printf("LongitudinalIndexStatusHandler GetLongitudinalIndexStatuses error: %v", err)
//		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "server_error"})
//		return
//	}
//
//	writeJSON(w, http.StatusOK, map[string]interface{}{
//		"ok":       true,
//		"statuses": statuses,
//	})
//}
