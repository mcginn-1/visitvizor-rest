package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/firestore"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ProviderUploadToken allows a patient to authorize a provider/imaging center
// to upload imaging on their behalf using a short code plus phone number.
type ProviderUploadToken struct {
	TokenID       string    `firestore:"token_id" json:"token_id"`
	UserID        string    `firestore:"user_id" json:"user_id"`
	PhoneHash     string    `firestore:"phone_hash" json:"phone_hash"`
	ExpiresAt     time.Time `firestore:"expires_at" json:"expires_at"`
	RemainingUses int       `firestore:"remaining_uses" json:"remaining_uses"`
	Revoked       bool      `firestore:"revoked" json:"revoked"`
	CreatedAt     time.Time `firestore:"created_at" json:"created_at"`
}

// UploadSession tracks a single imaging upload session (for GCS/DICOM).
type UploadSession struct {
	SessionID string    `firestore:"session_id" json:"session_id"`
	UserID    string    `firestore:"user_id" json:"user_id"`
	CreatedBy string    `firestore:"created_by" json:"created_by"` // "patient" or "provider"
	Status    string    `firestore:"status" json:"status"`         // pending|uploading|uploaded|importing|ready|error
	GCSURI    string    `firestore:"gcs_uri" json:"gcs_uri"`
	GCSPrefix string    `firestore:"gcs_prefix" json:"gcs_prefix"` // e.g. "gs://bucket/userId/sessionId/"

	DicomImportOpName string    `firestore:"dicom_import_operation" json:"dicom_import_operation"` // LRO name from Healthcare
	ErrorMsg          string    `firestore:"error_message" json:"error_message"`
	CreatedAt         time.Time `firestore:"created_at" json:"created_at"`
	UpdatedAt         time.Time `firestore:"updated_at" json:"updated_at"`
}

// normalizePhone does a very simple phone normalization: strips non-digits
// and preserves a leading '+' if present.
func normalizePhone(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}

	// Keep leading + if present, strip all non-digits otherwise
	hasPlus := strings.HasPrefix(s, "+")
	re := regexp.MustCompile(`[^0-9]+`)
	s = re.ReplaceAllString(s, "")
	if hasPlus {
		return "+" + s
	}
	return s
}

// hashPhone hashes the normalized phone with SHA-256.
func hashPhone(normalized string) string {
	h := sha256.Sum256([]byte(normalized))
	// Base32 for readability if we ever need to inspect it.
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:])
}

// randomTokenID generates a short, human-friendly token like UPL-XXXXX.
func randomTokenID(prefix string, nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}
	id := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	// Trim to something reasonable like 8 chars
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s-%s", prefix, id), nil
}

// CreateProviderUploadToken stores a new ProviderUploadToken in Firestore.
func (db *FirestoreDB) CreateProviderUploadToken(ctx context.Context, t *ProviderUploadToken) error {
	if t == nil {
		return fmt.Errorf("nil token")
	}
	if t.TokenID == "" {
		return fmt.Errorf("missing token_id")
	}
	// Full document write for provider upload tokens; we always send complete data.
	_, err := db.client.Collection("provider_upload_tokens").Doc(t.TokenID).Set(ctx, t)
	if err != nil {
		return fmt.Errorf("create provider upload token (%s): %w", t.TokenID, err)
	}
	return nil
}

// GetProviderUploadToken fetches a ProviderUploadToken by its ID.
func (db *FirestoreDB) GetProviderUploadToken(ctx context.Context, tokenID string) (*ProviderUploadToken, error) {
	snap, err := db.client.Collection("provider_upload_tokens").Doc(tokenID).Get(ctx)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get provider upload token (%s): %w", tokenID, err)
	}
	var t ProviderUploadToken
	if err := snap.DataTo(&t); err != nil {
		return nil, fmt.Errorf("decode provider upload token (%s): %w", tokenID, err)
	}
	return &t, nil
}

// UpdateProviderUploadToken performs a partial update (merge).
func (db *FirestoreDB) UpdateProviderUploadToken(ctx context.Context, tokenID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	_, err := db.client.Collection("provider_upload_tokens").Doc(tokenID).Set(ctx, updates, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("update provider upload token (%s): %w", tokenID, err)
	}
	return nil
}

// CreateUploadSession stores a new UploadSession document.
func (db *FirestoreDB) CreateUploadSession(ctx context.Context, s *UploadSession) error {
	if s == nil {
		return fmt.Errorf("nil session")
	}
	if s.SessionID == "" {
		return fmt.Errorf("missing session_id")
	}
	// Full document write for upload sessions; status can be updated later via UpdateUploadSessionStatus.
	_, err := db.client.Collection("upload_sessions").Doc(s.SessionID).Set(ctx, s)
	if err != nil {
		return fmt.Errorf("create upload session (%s): %w", s.SessionID, err)
	}
	return nil
}

// UpdateUploadSessionStatus updates status, GCS URI, or error message.
func (db *FirestoreDB) UpdateUploadSessionStatus(ctx context.Context, sessionID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now().UTC()
	_, err := db.client.Collection("upload_sessions").Doc(sessionID).Set(ctx, updates, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("update upload session (%s): %w", sessionID, err)
	}
	return nil
}

// GetUploadSession fetches an UploadSession by its ID.
func (db *FirestoreDB) GetUploadSession(ctx context.Context, sessionID string) (*UploadSession, error) {
	snap, err := db.client.Collection("upload_sessions").Doc(sessionID).Get(ctx)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get upload session (%s): %w", sessionID, err)
	}
	var s UploadSession
	if err := snap.DataTo(&s); err != nil {
		return nil, fmt.Errorf("decode upload session (%s): %w", sessionID, err)
	}
	return &s, nil
}

// ImagingStudy represents a logical imaging study (grouped by StudyInstanceUID)
// that can be listed and viewed in the frontend.
type ImagingStudy struct {
	StudyID            string    `firestore:"study_id" json:"study_id"`
	UserID             string    `firestore:"user_id" json:"user_id"`
	SessionID          string    `firestore:"session_id" json:"session_id"`

	StudyInstanceUID   string   `firestore:"study_instance_uid" json:"study_instance_uid"`
	SeriesInstanceUIDs []string `firestore:"series_instance_uids" json:"series_instance_uids"`
	ModalitiesInStudy  []string `firestore:"modalities_in_study" json:"modalities_in_study"`

	StudyDate        string `firestore:"study_date" json:"study_date"`
	StudyDescription string `firestore:"study_description" json:"study_description"`
	NumInstances     int    `firestore:"num_instances" json:"num_instances"`

	GCSPrefix      string    `firestore:"gcs_prefix" json:"gcs_prefix"`
	DicomStorePath string    `firestore:"dicom_store_path" json:"dicom_store_path"`
	CreatedAt      time.Time `firestore:"created_at" json:"created_at"`
}

// CreateImagingStudy stores a new ImagingStudy document.
func (db *FirestoreDB) CreateImagingStudy(ctx context.Context, s *ImagingStudy) error {
	if s == nil {
		return fmt.Errorf("nil imaging study")
	}
	if s.StudyID == "" {
		return fmt.Errorf("missing study_id")
	}
	_, err := db.client.Collection("imaging_studies").Doc(s.StudyID).Set(ctx, s)
	if err != nil {
		return fmt.Errorf("create imaging study (%s): %w", s.StudyID, err)
	}
	return nil
}

// GetImagingStudy fetches a single ImagingStudy by its StudyID.
func (db *FirestoreDB) GetImagingStudy(ctx context.Context, studyID string) (*ImagingStudy, error) {
	if strings.TrimSpace(studyID) == "" {
		return nil, fmt.Errorf("empty study_id")
	}
	ref := db.client.Collection("imaging_studies").Doc(studyID)
	snap, err := ref.Get(ctx)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get imaging study (%s): %w", studyID, err)
	}
	var s ImagingStudy
	if err := snap.DataTo(&s); err != nil {
		return nil, fmt.Errorf("decode imaging study (%s): %w", studyID, err)
	}
	return &s, nil
}

// ListImagingStudiesByUser returns all imaging studies for the given user,
// ordered by created_at descending.
func (db *FirestoreDB) ListImagingStudiesByUser(ctx context.Context, userID string) ([]*ImagingStudy, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("empty user_id")
	}

	q := db.client.Collection("imaging_studies").Where("user_id", "==", userID).
		OrderBy("created_at", firestore.Desc)

	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("list imaging studies for user %s: %w", userID, err)
	}

	studies := make([]*ImagingStudy, 0, len(docs))
	for _, snap := range docs {
		var s ImagingStudy
		if err := snap.DataTo(&s); err != nil {
			return nil, fmt.Errorf("decode imaging study (%s): %w", snap.Ref.ID, err)
		}
		studies = append(studies, &s)
	}
	return studies, nil
}

// GetImagingStudyByStudyInstanceUID returns the first ImagingStudy whose
// study_instance_uid matches the provided DICOM StudyInstanceUID.
func (db *FirestoreDB) GetImagingStudyByStudyInstanceUID(ctx context.Context, studyInstanceUID string) (*ImagingStudy, error) {
	studyInstanceUID = strings.TrimSpace(studyInstanceUID)
	if studyInstanceUID == "" {
		return nil, fmt.Errorf("empty study_instance_uid")
	}

	q := db.client.Collection("imaging_studies").Where("study_instance_uid", "==", studyInstanceUID).Limit(1)
	docs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("query imaging studies by StudyInstanceUID %s: %w", studyInstanceUID, err)
	}
	if len(docs) == 0 {
		return nil, nil
	}
	var s ImagingStudy
	if err := docs[0].DataTo(&s); err != nil {
		return nil, fmt.Errorf("decode imaging study by StudyInstanceUID %s: %w", studyInstanceUID, err)
	}
	return &s, nil
}
