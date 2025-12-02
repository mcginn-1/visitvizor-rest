package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Collection name: e.g. imaging_slice_index.
//
// Key strategy: let Firestore auto‑ID; query by study_id / frame_of_reference_uid later.
type IndexedSlice struct {
	// Ownership / grouping
	StudyID           string `firestore:"study_id" json:"study_id"`               // ImagingStudy.StudyID
	PatientUserID     string `firestore:"patient_user_id" json:"patient_user_id"` // ImagingStudy.UserID
	StudyInstanceUID  string `firestore:"study_instance_uid" json:"study_instance_uid"`
	SeriesInstanceUID string `firestore:"series_instance_uid" json:"series_instance_uid"`
	SOPInstanceUID    string `firestore:"sop_instance_uid" json:"sop_instance_uid"`
	InstanceNumber    int    `firestore:"instance_number" json:"instance_number"`

	FrameOfReferenceUID string `firestore:"frame_of_reference_uid" json:"frame_of_reference_uid"`

	// Geometry
	IPPX, IPPY, IPPZ          float64 `firestore:"ipp_x" json:"ipp_x"`
	RowDirX, RowDirY, RowDirZ float64 `firestore:"row_dir_x" json:"row_dir_x"`
	ColDirX, ColDirY, ColDirZ float64 `firestore:"col_dir_x" json:"col_dir_x"`
	RowSpacing, ColSpacing    float64 `firestore:"row_spacing" json:"row_spacing"`

	// Precomputed plane: normal · x = d
	NormalX, NormalY, NormalZ float64 `firestore:"normal_x" json:"normal_x"`
	PlaneD                    float64 `firestore:"plane_d" json:"plane_d"`

	StudyDate       string    `firestore:"study_date" json:"study_date"`
	AcquisitionTime string    `firestore:"acquisition_time" json:"acquisition_time"`
	CreatedAt       time.Time `firestore:"created_at" json:"created_at"`
}

// collection logitudinal_index_status
type LongitudinalIndexStatus struct {
	StudyID       string    `firestore:"study_id" json:"study_id"`
	PatientUserID string    `firestore:"patient_user_id" json:"patient_user_id"`
	Status        string    `firestore:"status" json:"status"` // "not_indexed" | "indexing" | "indexed" | "error"
	LastError     string    `firestore:"last_error" json:"last_error"`
	UpdatedAt     time.Time `firestore:"updated_at" json:"updated_at"`
}

// ////////////////////////////////////
//
//	Extending FirestoreDB
//
//	 Save indexed slices - table: imaging_slice_index
func (db *FirestoreDB) SaveIndexedSlicesForStudy(
	ctx context.Context,
	studyID string,
	slices []*IndexedSlice,
) error {
	if studyID == "" {
		return fmt.Errorf("empty studyID")
	}
	// For simplicity, delete existing index for this study then write new docs.
	// You can optimize later.
	col := db.client.Collection("imaging_slice_index")
	// Delete old docs
	q := col.Where("study_id", "==", studyID)
	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("query existing index: %w", err)
	}
	batch := db.client.Batch()
	for _, d := range docs {
		batch.Delete(d.Ref)
	}
	if _, err := batch.Commit(ctx); err != nil {
		return fmt.Errorf("delete old index: %w", err)
	}

	if len(slices) == 0 {
		return nil
	}

	// Write new docs in batches of ~400‑500 to avoid Firestore limits.
	const batchSize = 400
	for i := 0; i < len(slices); i += batchSize {
		end := i + batchSize
		if end > len(slices) {
			end = len(slices)
		}
		b := db.client.Batch()
		for _, s := range slices[i:end] {
			ref := col.NewDoc()
			b.Set(ref, s)
		}
		if _, err := b.Commit(ctx); err != nil {
			return fmt.Errorf("write index batch: %w", err)
		}
	}
	return nil
}

// ListIndexedSlicesForStudyAndFoR returns all indexed slices for a given
// study_id and FrameOfReferenceUID. This is used by the resolve-point
// endpoint to find candidate slices per study.
func (db *FirestoreDB) ListIndexedSlicesForStudyAndFoR(
	ctx context.Context,
	studyID string,
	frameOfRefUID string,
) ([]*IndexedSlice, error) {
	studyID = strings.TrimSpace(studyID)
	frameOfRefUID = strings.TrimSpace(frameOfRefUID)
	if studyID == "" || frameOfRefUID == "" {
		return nil, fmt.Errorf("studyID and frameOfRefUID are required")
	}

	col := db.client.Collection("imaging_slice_index")
	q := col.
		Where("study_id", "==", studyID).
		Where("frame_of_reference_uid", "==", frameOfRefUID)

	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("query indexed slices for study %s: %w", studyID, err)
	}

	res := make([]*IndexedSlice, 0, len(docs))
	for _, d := range docs {
		var s IndexedSlice
		if err := d.DataTo(&s); err != nil {
			return nil, fmt.Errorf("decode indexed slice (%s): %w", d.Ref.ID, err)
		}
		res = append(res, &s)
	}
	return res, nil
}

// ////////////////////////////////////
//
//	Extending FirestoreDB - table: imaging_longitudinal_status
//
//	 Set Status of per study indexes
func (db *FirestoreDB) SetLongitudinalIndexStatus(
	ctx context.Context,
	status *LongitudinalIndexStatus,
) error {
	if status == nil || strings.TrimSpace(status.StudyID) == "" {
		return fmt.Errorf("invalid status")
	}
	status.UpdatedAt = time.Now().UTC()
	_, err := db.client.Collection("imaging_longitudinal_status").
		Doc(status.StudyID).Set(ctx, status)
	if err != nil {
		return fmt.Errorf("set longitudinal index status: %w", err)
	}
	return nil
}

// ////////////////////////////////////
//
//	Extending FirestoreDB - table: imaging_longitudinal_status
//
//	 Get Status of per study indexes
func (db *FirestoreDB) GetLongitudinalIndexStatuses(
	ctx context.Context,
	studyIDs []string,
) (map[string]*LongitudinalIndexStatus, error) {
	result := make(map[string]*LongitudinalIndexStatus, len(studyIDs))
	if len(studyIDs) == 0 {
		return result, nil
	}
	col := db.client.Collection("imaging_longitudinal_status")
	for _, id := range studyIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		snap, err := col.Doc(id).Get(ctx)
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				continue
			}
			return nil, fmt.Errorf("get index status %s: %w", id, err)
		}
		var s LongitudinalIndexStatus
		if err := snap.DataTo(&s); err != nil {
			return nil, fmt.Errorf("decode index status %s: %w", id, err)
		}
		result[id] = &s
	}
	return result, nil
}

// ////////////////////////////////////////////////////////////
//
//	Indexing function: DICOM metadata → IndexedSlices
//
//	We already parse DICOM JSON in handleDicomWebListInstances /
//	 handleDicomWebSeriesMetadata using dicomwebTagString.
//	 We reuse that pattern here.
//
//	This is the PARSER. Then you will use the INDEXER (buildIndexedSlicesForStudy).
//
// parseDICOMFloatSlice parses a DICOM JSON element that contains a list of numbers (e.g. 0020,0032 or 0020,0037).
func parseDICOMFloatSlice(ds map[string]interface{}, tag string, expected int) ([]float64, bool) {
	v, ok := ds[tag]
	if !ok {
		return nil, false
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, false
	}
	raw, ok := m["Value"]
	if !ok {
		return nil, false
	}
	vals, ok := raw.([]interface{})
	if !ok || len(vals) == 0 {
		return nil, false
	}
	res := make([]float64, 0, len(vals))
	for _, x := range vals {
		switch t := x.(type) {
		case float64:
			res = append(res, t)
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err == nil {
				res = append(res, f)
			}
		}
	}
	if expected > 0 && len(res) != expected {
		return nil, false
	}
	return res, true
}

// ////////////////////////////////////////////////////////////
//
//	     Calculator function: IndexedSlices →  Calculate // Row/col direction vectors
//	      											    // Normal = rowDir × colDir
//													    	// PlaneD = normal · IPP
//
//	     This is the VALUE CALCULATOR
func (h *Handlers) buildIndexedSlicesForStudy(
	ctx context.Context,
	study *ImagingStudy,
) ([]*IndexedSlice, error) {
	if h.Dicom == nil {
		return nil, fmt.Errorf("dicom client not configured")
	}
	bytes, err := h.Dicom.StudyMetadataJSON(ctx, study.StudyInstanceUID)
	if err != nil {
		return nil, fmt.Errorf("StudyMetadataJSON: %w", err)
	}

	var datasets []map[string]interface{}
	if err := json.Unmarshal(bytes, &datasets); err != nil {
		return nil, fmt.Errorf("unmarshal study metadata: %w", err)
	}

	slices := make([]*IndexedSlice, 0, len(datasets))
	now := time.Now().UTC()

	for _, ds := range datasets {
		seriesUID := dicomwebTagString(ds, "0020000E") // SeriesInstanceUID
		sopUID := dicomwebTagString(ds, "00080018")    // SOPInstanceUID
		if seriesUID == "" || sopUID == "" {
			continue
		}

		ipp, ok := parseDICOMFloatSlice(ds, "00200032", 3) // ImagePositionPatient
		if !ok {
			continue
		}
		iop, ok := parseDICOMFloatSlice(ds, "00200037", 6) // ImageOrientationPatient
		if !ok {
			continue
		}
		spacing, ok := parseDICOMFloatSlice(ds, "00280030", 2) // PixelSpacing
		if !ok {
			continue
		}

		// FrameOfReferenceUID
		forUID := dicomwebTagString(ds, "00200052")

		// Row/col direction vectors
		rowDir := [3]float64{iop[0], iop[1], iop[2]}
		colDir := [3]float64{iop[3], iop[4], iop[5]}

		// Normal = rowDir × colDir
		normal := [3]float64{
			rowDir[1]*colDir[2] - rowDir[2]*colDir[1],
			rowDir[2]*colDir[0] - rowDir[0]*colDir[2],
			rowDir[0]*colDir[1] - rowDir[1]*colDir[0],
		}
		// PlaneD = normal · IPP
		planeD := normal[0]*ipp[0] + normal[1]*ipp[1] + normal[2]*ipp[2]

		// Instance number (optional)
		instNumStr := dicomwebTagString(ds, "00200013")
		instNum := 0
		if instNumStr != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(instNumStr)); err == nil {
				instNum = n
			}
		}

		s := &IndexedSlice{
			StudyID:             study.StudyID,
			PatientUserID:       study.UserID,
			StudyInstanceUID:    study.StudyInstanceUID,
			SeriesInstanceUID:   seriesUID,
			SOPInstanceUID:      sopUID,
			InstanceNumber:      instNum,
			FrameOfReferenceUID: forUID,

			IPPX: ipp[0], IPPY: ipp[1], IPPZ: ipp[2],
			RowDirX: rowDir[0], RowDirY: rowDir[1], RowDirZ: rowDir[2],
			ColDirX: colDir[0], ColDirY: colDir[1], ColDirZ: colDir[2],
			RowSpacing: spacing[0], ColSpacing: spacing[1],
			NormalX: normal[0], NormalY: normal[1], NormalZ: normal[2],
			PlaneD: planeD,

			StudyDate:       study.StudyDate,
			AcquisitionTime: "", // you can extract 0008,0032 if needed
			CreatedAt:       now,
		}
		slices = append(slices, s)
	}
	return slices, nil
}

//////////////////////////////////////////////////////////////
//
//  Distance and projection helpers for resolve-point
//

// distanceToSlicePlane returns the absolute distance from a point to the
// slice plane using the stored normal and PlaneD. Units are in the same
// space as the coordinates (typically mm).
func distanceToSlicePlane(slice *IndexedSlice, x, y, z float64) float64 {
	val := slice.NormalX*x + slice.NormalY*y + slice.NormalZ*z - slice.PlaneD
	return math.Abs(val)
}

// projectPointToSlice computes approximate pixel coordinates (row, col) of a
// world-space point on a given slice, using the stored geometry. We do not
// clamp to image bounds here; the viewer can handle clamping.
func projectPointToSlice(slice *IndexedSlice, x, y, z float64) (row, col float64) {
	vx := x - slice.IPPX
	vy := y - slice.IPPY
	vz := z - slice.IPPZ

	rowNumerator := vx*slice.RowDirX + vy*slice.RowDirY + vz*slice.RowDirZ
	colNumerator := vx*slice.ColDirX + vy*slice.ColDirY + vz*slice.ColDirZ

	if slice.RowSpacing != 0 {
		row = rowNumerator / slice.RowSpacing
	}
	if slice.ColSpacing != 0 {
		col = colNumerator / slice.ColSpacing
	}
	return row, col
}
