package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	//"cloud.google.com/go/storage"
	//"encoding/json"
	//"fmt"
	//"log"
	//"net/http"
	//"strings"
	//"time"
	//
	//"cloud.google.com/go/storage"

	"cloud.google.com/go/storage"
)

// writeJSON is a small helper to send JSON responses with status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON error: %v", err)
	}
}

// devAuthOK mirrors _dev_auth_ok() in routes_auth.py.
func (h *Handlers) devAuthOK(r *http.Request) bool {
	if h.Cfg.DevBearer == "" {
		return false
	}
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	return token == h.Cfg.DevBearer
}

// firstNonEmpty returns the first non-empty string from args.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// getUserIDFromRequest returns the effective user ID for this request.
//
// Priority:
//  1. If X-User-Id header is set (non-empty), trust and return it.
//     This is useful for local/dev flows and automated tests.
//  2. Otherwise, require Authorization: Bearer <Firebase ID token>
//     and verify it via Firebase Admin SDK.
//
// If no valid user can be determined, it returns an error.
func (h *Handlers) GetUserIDFromRequest(ctx context.Context, r *http.Request) (string, error) {
	// Dev/test override: X-User-Id short-circuits Firebase verification.
	if userID := strings.TrimSpace(r.Header.Get("X-User-Id")); userID != "" {
		return userID, nil
	}

	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		return "", fmt.Errorf("missing Authorization bearer token")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	if token == "" {
		return "", fmt.Errorf("empty bearer token")
	}

	decoded, err := h.verifyIDToken(ctx, token)
	if err != nil || decoded == nil {
		return "", fmt.Errorf("verifyIDToken failed: %w", err)
	}

	return decoded.UID, nil
}

// LoginHandler implements POST /api/login.
//
// Behavior matches routes_auth.py:
//   - Requires Authorization: Bearer <token>
//   - Tries Firebase ID token verification first; if successful, looks up
//     the account in Firestore and returns user info or account_not_found.
//   - On Firebase verification failure, falls back to dev bearer mode
//     when AUTH_DEV_BEARER matches the Authorization header.
//   - Otherwise returns {"ok": false, "error": "unauthorized"} with 401.
func (h *Handlers) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"ok":    false,
			"error": "Missing Authorization header",
		})
		return
	}

	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	ctx := r.Context()

	// Try Firebase token verification first
	if token != "" {
		decoded, err := h.verifyIDToken(ctx, token)
		if err == nil && decoded != nil {
			userID := decoded.UID
			var email string
			if e, ok := decoded.Claims["email"].(string); ok {
				email = e
			}

			// Get user info from database
			acc, err := h.DB.GetAccount(ctx, userID)
			if err != nil {
				log.Printf("GetAccount error: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok":    false,
					"error": "server_error",
				})
				return
			}

			if acc != nil {
				// User exists in database, return their info
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"ok":            true,
					"user_id":       userID,
					"email":         email,
					"first_name":    acc.FirstName,
					"last_name":     acc.LastName,
					"business_name": acc.BusinessName,
				})
				return
			}

			// User authenticated with Firebase but account doesn't exist in database
			// They need to sign up (not just login)
			writeJSON(w, http.StatusNotFound, map[string]interface{}{
				"ok":      false,
				"error":   "account_not_found",
				"message": "Please sign up to create an account",
			})
			return
		}

		if err != nil {
			log.Printf("Firebase token verification error: %v", err)
		}
	}

	// Fallback: Dev bearer token for testing
	if h.devAuthOK(r) {
		var body struct {
			UserID string `json:"user_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body) // silent, like get_json(silent=True)

		userID := strings.TrimSpace(firstNonEmpty(body.UserID, r.Header.Get("X-User-Id")))

		var userIDVal interface{}
		if userID == "" {
			userIDVal = nil
		} else {
			userIDVal = userID
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         true,
			"user_id":    userIDVal,
			"first_name": "Test",
			"last_name":  "User",
			"dev_mode":   true,
		})
		return
	}

	writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
		"ok":    false,
		"error": "unauthorized",
	})
}

// AccountsHandler implements POST /api/accounts (create account).
func (h *Handlers) AccountsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		UserID            string  `json:"user_id"`
		UserFirstName     string  `json:"user_first_name"`
		UserLastName      string  `json:"user_last_name"`
		UserBusiness      string  `json:"user_business_name"`
		Email             string  `json:"email"`
		UserPhone         string  `json:"user_phone"`
		UserAuthenticated *bool   `json:"user_authenticated"`
		UserLastLogin     *string `json:"user_last_login"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid_json",
		})
		return
	}

	userID := strings.TrimSpace(body.UserID)
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "user_id required",
		})
		return
	}

	firstName := strings.TrimSpace(body.UserFirstName)
	lastName := strings.TrimSpace(body.UserLastName)
	businessName := strings.TrimSpace(body.UserBusiness)
	email := strings.TrimSpace(body.Email)
	phone := strings.TrimSpace(body.UserPhone)

	authenticated := true
	if body.UserAuthenticated != nil {
		authenticated = *body.UserAuthenticated
	}

	// created_at similar to datetime.now(timezone.utc).isoformat()
	createdAt := time.Now().UTC().Format(time.RFC3339)

	acc := &Account{
		UserID:        userID,
		FirstName:     firstName,
		LastName:      lastName,
		BusinessName:  businessName,
		Email:         email,
		Phone:         phone,
		Authenticated: authenticated,
		LastLogin:     body.UserLastLogin,
		CreatedAt:     createdAt,
	}

	ctx := r.Context()
	if err := h.DB.CreateAccount(ctx, userID, acc); err != nil {
		log.Printf("CreateAccount error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	// Return full user info like /login does
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":            true,
		"user_id":       userID,
		"email":         email,
		"first_name":    firstName,
		"last_name":     lastName,
		"business_name": businessName,
		"phone":         phone,
	})
}

// AccountsMeHandler implements PUT /api/accounts/me to update the
// currently authenticated patient's profile (name, email, phone, etc.).
func (h *Handlers) AccountsMeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "Missing Authorization header",
		})
		return
	}

	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	ctx := r.Context()
	decoded, err := h.verifyIDToken(ctx, token)
	if err != nil || decoded == nil {
		log.Printf("AccountsMe verify token error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	userID := decoded.UID

	var body struct {
		UserFirstName *string `json:"user_first_name"`
		UserLastName  *string `json:"user_last_name"`
		UserBusiness  *string `json:"user_business_name"`
		Email         *string `json:"email"`
		UserPhone     *string `json:"user_phone"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid_json",
		})
		return
	}

	updates := map[string]interface{}{}
	if body.UserFirstName != nil {
		updates["first_name"] = strings.TrimSpace(*body.UserFirstName)
	}
	if body.UserLastName != nil {
		updates["last_name"] = strings.TrimSpace(*body.UserLastName)
	}
	if body.UserBusiness != nil {
		updates["business_name"] = strings.TrimSpace(*body.UserBusiness)
	}
	if body.Email != nil {
		updates["email"] = strings.TrimSpace(*body.Email)
	}
	if body.UserPhone != nil {
		updates["phone"] = strings.TrimSpace(*body.UserPhone)
	}

	if err := h.DB.UpdateAccount(ctx, userID, updates); err != nil {
		log.Printf("AccountsMe UpdateAccount error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"user_id": userID,
	})
}

// CreateProviderUploadTokenHandler implements POST /api/imaging/provider-tokens
// for patients to generate a short upload code providers can use.
func (h *Handlers) CreateProviderUploadTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("CreateProviderUploadTokenHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	var body struct {
		PatientPhone  string `json:"patient_phone"`
		ExpiresInDays int    `json:"expires_in_days"`
		MaxUses       int    `json:"max_uses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid_json",
		})
		return
	}

	phoneNorm := normalizePhone(body.PatientPhone)
	if phoneNorm == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "patient_phone required",
		})
		return
	}

	expiresDays := body.ExpiresInDays
	if expiresDays <= 0 {
		expiresDays = 7
	}
	maxUses := body.MaxUses
	if maxUses <= 0 {
		maxUses = 3
	}

	id, err := randomTokenID("UPL", 6)
	if err != nil {
		log.Printf("CreateProviderUploadToken randomTokenID error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	now := time.Now().UTC()
	t := &ProviderUploadToken{
		TokenID:       id,
		UserID:        userID,
		PhoneHash:     hashPhone(phoneNorm),
		ExpiresAt:     now.Add(time.Duration(expiresDays) * 24 * time.Hour),
		RemainingUses: maxUses,
		Revoked:       false,
		CreatedAt:     now,
	}

	if err := h.DB.CreateProviderUploadToken(ctx, t); err != nil {
		log.Printf("CreateProviderUploadToken DB error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":           true,
		"upload_token": t.TokenID,
	})
}

// ProviderCreateUploadSessionHandler implements
// POST /api/imaging/provider/upload-sessions for providers to start
// an imaging upload using patient_phone + upload_token.
func (h *Handlers) ProviderCreateUploadSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		PatientPhone string `json:"patient_phone"`
		UploadToken  string `json:"upload_token"`
		Modality     string `json:"modality"`
		Description  string `json:"description"`
		StudyDate    string `json:"study_date"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid_json",
		})
		return
	}

	phoneNorm := normalizePhone(body.PatientPhone)
	if phoneNorm == "" || strings.TrimSpace(body.UploadToken) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "patient_phone and upload_token required",
		})
		return
	}

	ctx := r.Context()
	t, err := h.DB.GetProviderUploadToken(ctx, strings.TrimSpace(body.UploadToken))
	if err != nil {
		log.Printf("ProviderCreateUploadSession GetProviderUploadToken error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if t == nil || t.Revoked || time.Now().After(t.ExpiresAt) || t.RemainingUses <= 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "invalid_or_expired_token",
		})
		return
	}

	if t.PhoneHash != hashPhone(phoneNorm) {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "phone_mismatch",
		})
		return
	}

	// Decrement RemainingUses (simple non-transactional update for now).
	if err := h.DB.UpdateProviderUploadToken(ctx, t.TokenID, map[string]interface{}{
		"remaining_uses": t.RemainingUses - 1,
	}); err != nil {
		log.Printf("ProviderCreateUploadSession UpdateProviderUploadToken error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	// TODO: wire in GCS resumable upload URL generation. For now, we
	// just allocate a session ID and record it.
	sessionID, err := randomTokenID("SESS", 10)
	if err != nil {
		log.Printf("ProviderCreateUploadSession randomTokenID error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	now := time.Now().UTC()
	sess := &UploadSession{
		SessionID: sessionID,
		UserID:    t.UserID,
		CreatedBy: "provider",
		Status:    "pending",
		GCSURI:    "", // to be filled when GCS integration is added
		CreatedAt: now,
		UpdatedAt: now,
	}

	log.Printf("DEBUG: creating upload session %s for user %s", sessionID, t.UserID)

	if err := h.DB.CreateUploadSession(ctx, sess); err != nil {
		log.Printf("ProviderCreateUploadSession CreateUploadSession error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"session_id": sessionID,
		// "upload_url" will be added once GCS signed resumable URLs are implemented
	})
}

// AccountsByIDHandler implements DELETE /api/accounts/<user_id>.
func (h *Handlers) AccountsByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Path is /api/accounts/<user_id>
	path := r.URL.Path
	const prefix = "/api/accounts/"
	if !strings.HasPrefix(path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	userID := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "user_id required",
		})
		return
	}

	ctx := context.Background()
	if err := h.DB.DeleteAccount(ctx, userID); err != nil {
		log.Printf("DeleteAccount error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"user_id": userID,
	})
}

// ProviderUploadFilesHandler implements POST /api/imaging/provider/upload/:session_id
// and accepts one or more files via multipart/form-data. For now it reads the
// uploaded data and discards it, but returns per-file success/failure so the
// uploader can see what happened. This is the place to plug in GCS/DICOM
// persistence later.  (old way, new way uses url)
func (h *Handlers) ProviderUploadFilesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Path: /api/imaging/provider/upload/<session_id>
	path := r.URL.Path
	const prefix = "/api/imaging/provider/upload/"
	if !strings.HasPrefix(path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	sessionID := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "session_id required",
		})
		return
	}

	ctx := r.Context()
	sess, err := h.DB.GetUploadSession(ctx, sessionID)
	if err != nil {
		log.Printf("ProviderUploadFiles GetUploadSession error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "session_not_found",
		})
		return
	}

	// Parse multipart form (limit to 512MB in memory/temporary files)
	if err := r.ParseMultipartForm(512 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid_multipart",
		})
		return
	}

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "no_files_provided",
		})
		return
	}

	results := make([]map[string]interface{}, 0, len(files))

	for _, fh := range files {
		res := map[string]interface{}{
			"file_name": fh.Filename,
		}

		f, err := fh.Open()
		if err != nil {
			res["ok"] = false
			res["error"] = err.Error()
			results = append(results, res)
			continue
		}

		// For now we stream the content into our private GCS bucket. Later this
		// path can be wired to a DICOM import pipeline.
		objectPath := fmt.Sprintf("%s/%s/%s", sess.UserID, sessionID, fh.Filename)
		w := h.Storage.Bucket(h.Cfg.ImagingBucket).Object(objectPath).NewWriter(ctx)
		if _, err := io.Copy(w, f); err != nil {
			res["ok"] = false
			res["error"] = err.Error()
		} else if err := w.Close(); err != nil {
			res["ok"] = false
			res["error"] = err.Error()
		} else {
			res["ok"] = true
		}
		_ = f.Close()

		results = append(results, res)
	}

	// Mark session as uploaded; GCS/DICOM integration can refine this later.
	if err := h.DB.UpdateUploadSessionStatus(ctx, sessionID, map[string]interface{}{
		"status": "uploaded",
	}); err != nil {
		log.Printf("ProviderUploadFiles UpdateUploadSessionStatus error: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"session_id": sessionID,
		"results":    results,
	})

	// handlers.go additions

} // End provider uploads file handler (old way)

func sanitizeObjectName(name string) string {
	// simple, conservative sanitizer for object names
	name = strings.TrimSpace(name)
	if name == "" {
		return "unnamed"
	}
	// Replace backslashes with forward slashes and collapse multiple slashes.
	name = strings.ReplaceAll(name, "\\", "/")
	for strings.Contains(name, "//") {
		name = strings.ReplaceAll(name, "//", "/")
	}
	return name
}

// ProviderUploadURLHandler implements POST /api/imaging/provider/upload-url.
// It returns a signed URL so the imaging facility can upload directly to GCS
// for a given upload session.
func (h *Handlers) ProviderUploadURLHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionID   string `json:"session_id"`
		FileName    string `json:"file_name"`
		ContentType string `json:"content_type"`
		SizeBytes   int64  `json:"size_bytes"`
	}

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid_json",
		})
		return
	}

	body.SessionID = strings.TrimSpace(body.SessionID)
	body.FileName = strings.TrimSpace(body.FileName)
	if body.SessionID == "" || body.FileName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "session_id and file_name required",
		})
		return
	}

	ctx := r.Context()
	sess, err := h.DB.GetUploadSession(ctx, body.SessionID)
	if err != nil {
		log.Printf("ProviderUploadURL GetUploadSession error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "session_not_found",
		})
		return
	}

	// Optional: enforce that only provider-created sessions can use this.
	if sess.CreatedBy != "provider" {
		writeJSON(w, http.StatusForbidden, map[string]interface{}{
			"error": "forbidden_for_this_session",
		})
		return
	}

	// Optional: if you want to gate by status.
	if sess.Status != "pending" && sess.Status != "uploading" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "session_not_accepting_uploads",
		})
		return
	}

	safeName := sanitizeObjectName(body.FileName)

	// Object path: user_id/session_id/relative-path
	objectPath := fmt.Sprintf("%s/%s/%s", sess.UserID, sess.SessionID, safeName)

	if h.Cfg.SignedURLServiceAccountEmail == "" || h.Cfg.SignedURLPrivateKey == "" {
		log.Printf("ProviderUploadURL missing signed URL credentials in config")
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "signed_url_not_configured",
		})
		return
	}

	signedURL, err := storage.SignedURL(
		h.Cfg.ImagingBucket,
		objectPath,
		&storage.SignedURLOptions{
			Scheme:         storage.SigningSchemeV4,
			Method:         "PUT",
			Expires:        time.Now().Add(30 * time.Minute),
			ContentType:    body.ContentType,
			GoogleAccessID: h.Cfg.SignedURLServiceAccountEmail,
			PrivateKey:     []byte(h.Cfg.SignedURLPrivateKey),
		},
	)
	if err != nil {
		log.Printf("ProviderUploadURL SignedURL error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "failed_to_generate_upload_url",
		})
		return
	}

	// gs:// path for later DICOM import / viewing.
	gsPath := fmt.Sprintf("gs://%s/%s", h.Cfg.ImagingBucket, objectPath)

	// Optional: mark session as "uploading" if it was "pending".
	if sess.Status == "pending" {
		if err := h.DB.UpdateUploadSessionStatus(ctx, sess.SessionID, map[string]interface{}{
			"status": "uploading",
		}); err != nil {
			log.Printf("ProviderUploadURL UpdateUploadSessionStatus error: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"uploadUrl": signedURL,
		"gsPath":    gsPath,
		"uploadId":  objectPath, // can serve as a per-file ID
	})
}

// handlers.go additions

// ProviderGetUploadSessionHandler implements
// GET /api/imaging/provider/upload-sessions/<session_id>
//
//	Returns a upload session object for front end consumption
func (h *Handlers) ProviderGetUploadSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	const prefix = "/api/imaging/provider/upload-sessions/"
	if !strings.HasPrefix(path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Get the {id} from the end of the url
	sessionID := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "session_id required",
		})
		return
	}

	log.Printf("DEBUG: ProviderGetUploadSessionHandler looking up session %s", sessionID)

	ctx := r.Context()
	sess, err := h.DB.GetUploadSession(ctx, sessionID)
	if err != nil {
		log.Printf("ProviderGetUploadSession GetUploadSession error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	if sess == nil {
		log.Printf("DEBUG: upload session %s not found in Firestore", sessionID)
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"error": "session_not_found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"session": sess,
	})
}

// ListImagingStudiesHandler implements GET /api/imaging/studies.
// It returns all ImagingStudy documents for the currently authenticated user.
func (h *Handlers) ListImagingStudiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("ListImagingStudiesHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	studies, err := h.DB.ListImagingStudiesByUser(ctx, userID)
	if err != nil {
		log.Printf("ListImagingStudies ListImagingStudiesByUser error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"studies": studies,
	})
}

// ImagingStudyByIDHandler implements:
//   - GET /api/imaging/studies/<study_id>
//   - GET /api/imaging/studies/<study_id>/dicom/metadata
//   - GET /api/imaging/studies/<study_id>/dicom/series/<seriesUID>/instances/<sopUID>/frames/<frame>
//
// It routes to the appropriate sub-handler based on the URL path. All
// variants require an authenticated user who owns the ImagingStudy record.
func (h *Handlers) ImagingStudyByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	const prefix = "/api/imaging/studies/"
	if !strings.HasPrefix(path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	suffix := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if suffix == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "study_id required",
		})
		return
	}

	parts := strings.Split(suffix, "/")

	// /api/imaging/studies/{studyID}
	if len(parts) == 1 {
		studyID := parts[0]
		h.handleImagingStudyJSON(w, r, studyID)
		return
	}

	// /api/imaging/studies/{studyID}/dicom/metadata
	if len(parts) == 3 && parts[1] == "dicom" && parts[2] == "metadata" {
		studyID := parts[0]
		h.handleImagingStudyDicomMetadata(w, r, studyID)
		return
	}

	// /api/imaging/studies/{studyID}/dicom/series/{seriesUID}/instances/{sopUID}/frames/{frame}
	if len(parts) == 8 && parts[1] == "dicom" && parts[2] == "series" && parts[4] == "instances" && parts[6] == "frames" {
		studyID := parts[0]
		seriesUID := parts[3]
		sopUID := parts[5]
		frame := parts[7]
		h.handleImagingStudyDicomFrame(w, r, studyID, seriesUID, sopUID, frame)
		return
	}

	// No known sub-route.
	w.WriteHeader(http.StatusNotFound)
}

// handleImagingStudyJSON returns the ImagingStudy document as JSON after
// verifying that the authenticated user owns it.
func (h *Handlers) handleImagingStudyJSON(w http.ResponseWriter, r *http.Request, studyID string) {
	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("handleImagingStudyJSON getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	study, err := h.DB.GetImagingStudy(ctx, studyID)
	if err != nil {
		log.Printf("ImagingStudyByID GetImagingStudy error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if study == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "study_not_found",
		})
		return
	}

	// Ensure the authenticated user owns this study.
	if study.UserID != userID {
		// Do not leak that the study exists for another user.
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "study_not_found",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"study": study,
	})
}

// handleImagingStudyDicomMetadata proxies DICOMweb /studies/{StudyInstanceUID}/metadata
// through the backend after verifying ownership of the ImagingStudy.
func (h *Handlers) handleImagingStudyDicomMetadata(w http.ResponseWriter, r *http.Request, studyID string) {
	if h.Dicom == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "dicom_client_not_configured",
		})
		return
	}

	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("handleImagingStudyDicomMetadata getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	study, err := h.DB.GetImagingStudy(ctx, studyID)
	if err != nil {
		log.Printf("handleImagingStudyDicomMetadata GetImagingStudy error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if study == nil || study.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "study_not_found",
		})
		return
	}

	bytes, err := h.Dicom.StudyMetadataJSON(ctx, study.StudyInstanceUID)
	if err != nil {
		log.Printf("handleImagingStudyDicomMetadata StudyMetadataJSON error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_metadata_error",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(bytes); err != nil {
		log.Printf("handleImagingStudyDicomMetadata write error: %v", err)
	}
}

// handleImagingStudyDicomFrame proxies a rendered frame/image for a single
// instance through the backend. For now, the frame index is accepted in the
// URL but currently renders the whole instance via RetrieveRendered.
func (h *Handlers) handleImagingStudyDicomFrame(w http.ResponseWriter, r *http.Request, studyID, seriesUID, sopUID, frame string) {
	if h.Dicom == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "dicom_client_not_configured",
		})
		return
	}

	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("handleImagingStudyDicomFrame getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	study, err := h.DB.GetImagingStudy(ctx, studyID)
	if err != nil {
		log.Printf("handleImagingStudyDicomFrame GetImagingStudy error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if study == nil || study.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "study_not_found",
		})
		return
	}

	// For now we trust seriesUID/sopUID; later you can verify that the series
	// belongs to this study using metadata.
	resp, err := h.Dicom.RetrieveRenderedInstanceJPEG(ctx, study.StudyInstanceUID, seriesUID, sopUID)
	if err != nil {
		log.Printf("handleImagingStudyDicomFrame RetrieveRenderedInstanceJPEG error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_render_error",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("handleImagingStudyDicomFrame upstream status %d %s", resp.StatusCode, resp.Status)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// Propagate JPEG content to the client.
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/jpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("handleImagingStudyDicomFrame io.Copy error: %v", err)
	}
}

// dicomwebTagString extracts the first string Value from a DICOM JSON dataset
// element keyed by the given tag (e.g. "0020000E" for SeriesInstanceUID).
func dicomwebTagString(ds map[string]interface{}, tag string) string {
	v, ok := ds[tag]
	if !ok {
		return ""
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	vals, ok := m["Value"].([]interface{})
	if !ok || len(vals) == 0 {
		return ""
	}
	if s, ok := vals[0].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// //////////////////////////////////////////////////////////
//
//	Extract orientation key for determining likeness in a series
//	so we can throw out any that are far different than standard orientation
//	for that series
//
// dicomwebOrientationKey extracts Image Orientation (Patient) (0020,0037)
// from a DICOM JSON dataset as a normalized string key like
// "1\0\0\0\1\0" so we can group by orientation.
func dicomwebOrientationKey(ds map[string]interface{}) string {
	v, ok := ds["00200037"]
	if !ok {
		return ""
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	raw, ok := m["Value"]
	if !ok {
		return ""
	}

	vals, ok := raw.([]interface{})
	if !ok || len(vals) == 0 {
		return ""
	}

	// Expect 6 numbers; normalize them into a simple string.
	parts := make([]string, 0, len(vals))
	for _, x := range vals {
		switch t := x.(type) {
		case string:
			parts = append(parts, strings.TrimSpace(t))
		case float64:
			// Format floats in a stable way
			parts = append(parts, fmt.Sprintf("%.6f", t))
		default:
			// ignore
		}
	}
	if len(parts) == 0 {
		return ""
	}
	// Join with a separator that won't appear in numbers.
	return strings.Join(parts, "\\")
}

// DicomWebStudiesHandler implements a minimal subset of DICOMweb paths under
// /api/dicomweb/studies/ for use by OHIF or other viewers. It supports:
//   - GET /api/dicomweb/studies/{StudyInstanceUID}/series
//   - GET /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances
//
// Both variants require an authenticated user who owns a Firestore
// ImagingStudy whose study_instance_uid matches the StudyInstanceUID.
func (h *Handlers) DicomWebStudiesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if h.Dicom == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "dicom_client_not_configured",
		})
		return
	}

	path := r.URL.Path
	const prefix = "/api/dicomweb/studies/"
	if !strings.HasPrefix(path, prefix) {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	////////////////////////////////////////////////////
	//
	//     Handling the /studies call from OHIF
	//
	//
	suffix := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if suffix == "" {
		h.handleDicomWebSearchStudies(w, r)
		//w.WriteHeader(http.StatusNotFound)
		return
	}

	parts := strings.Split(suffix, "/")
	if len(parts) < 2 || parts[1] != "series" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	studyUID := parts[0]

	// Auth + ownership check via Firestore ImagingStudy
	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("DicomWebStudiesHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	// Ensure the requested StudyInstanceUID corresponds to a study owned by
	// this user. We use Firestore as the source of truth for ownership.
	studyRec, err := h.DB.GetImagingStudyByStudyInstanceUID(ctx, studyUID)
	if err != nil {
		log.Printf("DicomWebStudiesHandler GetImagingStudyByStudyInstanceUID error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if studyRec == nil || studyRec.UserID != userID {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": "study_not_found",
		})
		return
	}

	// Decide which sub-route we are handling.
	if len(parts) == 2 {
		// /api/dicomweb/studies/{StudyInstanceUID}/series
		h.handleDicomWebListSeries(w, r, studyUID)
		return
	}
	if len(parts) == 4 && parts[1] == "series" && parts[3] == "metadata" {
		// /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/metadata
		seriesUID := parts[2]
		h.handleDicomWebSeriesMetadata(w, r, studyUID, seriesUID)
		return
	}
	if len(parts) == 4 && parts[2] != "" && parts[3] == "instances" {
		// /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances
		seriesUID := parts[2]
		h.handleDicomWebListInstances(w, r, studyUID, seriesUID)
		return
	}
	if len(parts) == 5 && parts[2] != "" && parts[3] == "instances" {
		// /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances/{SOPInstanceUID}
		seriesUID := parts[2]
		sopUID := parts[4]
		h.handleDicomWebRetrieveInstance(w, r, studyUID, seriesUID, sopUID, false)
		return
	}
	if len(parts) == 6 && parts[2] != "" && parts[3] == "instances" && parts[5] == "rendered" {
		// /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances/{SOPInstanceUID}/rendered
		seriesUID := parts[2]
		sopUID := parts[4]
		h.handleDicomWebRetrieveInstance(w, r, studyUID, seriesUID, sopUID, true)
		return
	}

	//////////////
	// old /frames handler
	//if len(parts) == 7 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "frames" {
	//	// /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances/{SOPInstanceUID}/frames/{frame}
	//	// For now we ignore the specific frame number and return the full instance.
	//	seriesUID := parts[2]
	//	sopUID := parts[4]
	//	h.handleDicomWebRetrieveInstance(w, r, studyUID, seriesUID, sopUID, false)
	//	return
	//}

	//////////////////////////////////////////////////
	//
	//   handles /frames endpoint  - for returning single Dicom frames
	//
	//     /frames originally returned full dicom, which was wrong, needs to return multipart
	//       this fixes that
	//
	//          Request headers like:
	//
	//           Response headers:
	//             content-type: multipart/related;
	//               boundary=d6c54d1f0a2b95e5c946e7a098460569c4590ac002bd35ddb4a287e9ab1c;
	//               transfer-syntax=*; type="application/octet-stream"
	//
	//           Each multipart part is now:
	//             Content-Type: application/octet-stream; transfer-syntax=1.2.840.10008.1.2.1
	//           i.e. raw PixelData bytes, NOT application/dicom.
	//
	//
	if len(parts) == 7 && parts[1] == "series" && parts[3] == "instances" && parts[5] == "frames" {
		// /api/dicomweb/studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances/{SOPInstanceUID}/frames/{frameList}
		seriesUID := parts[2]
		sopUID := parts[4]
		frameList := parts[6] // usually "1", but could be "1,2,3"

		h.handleDicomWebRetrieveFrames(w, r, studyUID, seriesUID, sopUID, frameList)
		return
	}

	w.WriteHeader(http.StatusNotFound)
}

// handleDicomWebListSeries returns a DICOM JSON array describing the series
// within the given StudyInstanceUID, derived from the study-level metadata.
func (h *Handlers) handleDicomWebListSeries(w http.ResponseWriter, r *http.Request, studyUID string) {
	ctx := r.Context()
	bytes, err := h.Dicom.StudyMetadataJSON(ctx, studyUID)
	if err != nil {
		log.Printf("handleDicomWebListSeries StudyMetadataJSON error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_metadata_error",
		})
		return
	}

	var datasets []map[string]interface{}
	if err := json.Unmarshal(bytes, &datasets); err != nil {
		log.Printf("handleDicomWebListSeries unmarshal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "invalid_dicom_metadata",
		})
		return
	}

	seriesMap := make(map[string]map[string]interface{})

	for _, ds := range datasets {
		seriesUID := dicomwebTagString(ds, "0020000E") // SeriesInstanceUID
		if seriesUID == "" {
			continue
		}
		if _, exists := seriesMap[seriesUID]; exists {
			continue
		}

		modality := dicomwebTagString(ds, "00080060") // Modality
		desc := dicomwebTagString(ds, "0008103E")     // SeriesDescription
		study := dicomwebTagString(ds, "0020000D")    // StudyInstanceUID
		if study == "" {
			study = studyUID
		}

		obj := map[string]interface{}{}

		obj["0020000D"] = map[string]interface{}{"vr": "UI", "Value": []string{study}}
		obj["0020000E"] = map[string]interface{}{"vr": "UI", "Value": []string{seriesUID}}
		if modality != "" {
			obj["00080060"] = map[string]interface{}{"vr": "CS", "Value": []string{modality}}
		}
		if desc != "" {
			obj["0008103E"] = map[string]interface{}{"vr": "LO", "Value": []string{desc}}
		}

		seriesMap[seriesUID] = obj
	}

	out := make([]map[string]interface{}, 0, len(seriesMap))
	for _, v := range seriesMap {
		out = append(out, v)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("handleDicomWebListSeries encode error: %v", err)
	}
}

// ///////////////////////////////////////////////////////////////
//
//	Handle /studies
//
// handleDicomWebSearchStudies implements a minimal QIDO-RS /studies search.
//
// It currently supports only StudyInstanceUID as a filter and returns a single
// DICOM JSON study object if the study exists and is owned by the caller.
func (h *Handlers) handleDicomWebSearchStudies(w http.ResponseWriter, r *http.Request) {
	// Auth header is required (same as other DICOMweb handlers)
	ctx := r.Context()
	userID, err := h.GetUserIDFromRequest(ctx, r)
	if err != nil {
		log.Printf("DicomWebSearchStudiesHandler getUserIDFromRequest error: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": "unauthorized",
		})
		return
	}

	// For now we only support StudyInstanceUID as a QIDO filter
	studyUID := r.URL.Query().Get("StudyInstanceUID")
	studyUID = strings.TrimSpace(studyUID)
	if studyUID == "" {
		// No filter or unsupported filter: return empty result set
		w.Header().Set("Content-Type", "application/dicom+json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
		return
	}

	// Enforce ownership via Firestore ImagingStudy
	studyRec, err := h.DB.GetImagingStudyByStudyInstanceUID(ctx, studyUID)
	if err != nil {
		log.Printf("handleDicomWebSearchStudies GetImagingStudyByStudyInstanceUID error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "server_error",
		})
		return
	}
	if studyRec == nil || studyRec.UserID != userID {
		// No visible studies for this user/filter
		w.Header().Set("Content-Type", "application/dicom+json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("[]"))
		return
	}

	// Build a minimal DICOM JSON study object.
	// Tags OHIF typically cares about here:
	// - 0020000D StudyInstanceUID
	// - 00080020 StudyDate
	// - 00080030 StudyTime (optional; we leave blank)
	// - 00081030 StudyDescription
	// - 00080060 ModalitiesInStudy
	obj := map[string]interface{}{}

	obj["0020000D"] = map[string]interface{}{
		"vr":    "UI",
		"Value": []string{studyRec.StudyInstanceUID},
	}
	if studyRec.StudyDate != "" {
		obj["00080020"] = map[string]interface{}{
			"vr":    "DA",
			"Value": []string{studyRec.StudyDate},
		}
	}
	if studyRec.StudyDescription != "" {
		obj["00081030"] = map[string]interface{}{
			"vr":    "LO",
			"Value": []string{studyRec.StudyDescription},
		}
	}
	if len(studyRec.ModalitiesInStudy) > 0 {
		obj["00080060"] = map[string]interface{}{
			"vr":    "CS",
			"Value": studyRec.ModalitiesInStudy,
		}
	}

	out := []map[string]interface{}{obj}

	w.Header().Set("Content-Type", "application/dicom+json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Printf("handleDicomWebSearchStudies encode error: %v", err)
	}
}

// handleDicomWebListInstances returns a DICOM JSON array describing the
// instances within the given StudyInstanceUID/SeriesInstanceUID, derived from
// the study-level metadata.
func (h *Handlers) handleDicomWebListInstances(w http.ResponseWriter, r *http.Request, studyUID, seriesUID string) {
	ctx := r.Context()
	bytes, err := h.Dicom.StudyMetadataJSON(ctx, studyUID)
	if err != nil {
		log.Printf("handleDicomWebListInstances StudyMetadataJSON error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_metadata_error",
		})
		return
	}

	var datasets []map[string]interface{}
	if err := json.Unmarshal(bytes, &datasets); err != nil {
		log.Printf("handleDicomWebListInstances unmarshal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "invalid_dicom_metadata",
		})
		return
	}

	/////////////////////////////////////////
	//
	// 1) Determine dominant orientation for this SeriesInstanceUID.
	//
	//   Takes out any outliers based on orientation
	//    such as those different orientated slides from our OHIF test
	//    should all be same orientation after this
	//
	orientationCounts := make(map[string]int)
	for _, ds := range datasets {
		sUID := dicomwebTagString(ds, "0020000E") // SeriesInstanceUID
		if sUID != seriesUID {
			continue
		}
		key := dicomwebOrientationKey(ds)
		if key == "" {
			continue
		}
		orientationCounts[key]++
	}
	// Calculate the dominant orientation
	dominantOrientation := ""
	maxCount := 0
	for key, count := range orientationCounts {
		if count > maxCount {
			maxCount = count
			dominantOrientation = key
		}
	}

	instances := make([]map[string]interface{}, 0)

	for _, ds := range datasets {
		sUID := dicomwebTagString(ds, "0020000E") // SeriesInstanceUID
		if sUID != seriesUID {
			continue
		}

		////////////////////////////////////////
		//
		//   Apply Dominant Orientation Filter
		//
		//    Should filter out any without the common orientation of
		//     the dominant set of slides, such as
		//     the first outliers we were seeing
		//
		if dominantOrientation != "" {
			key := dicomwebOrientationKey(ds)
			if key != "" && key != dominantOrientation {
				// Skip orientation outliers
				continue
			}
		}

		studyVal := dicomwebTagString(ds, "0020000D") // StudyInstanceUID
		if studyVal == "" {
			studyVal = studyUID
		}
		sopInstanceUID := dicomwebTagString(ds, "00080018") // SOPInstanceUID
		if sopInstanceUID == "" {
			continue
		}
		instanceNumber := dicomwebTagString(ds, "00200013") // InstanceNumber
		modality := dicomwebTagString(ds, "00080060")       // Modality

		obj := map[string]interface{}{}
		obj["0020000D"] = map[string]interface{}{"vr": "UI", "Value": []string{studyVal}}
		obj["0020000E"] = map[string]interface{}{"vr": "UI", "Value": []string{seriesUID}}
		obj["00080018"] = map[string]interface{}{"vr": "UI", "Value": []string{sopInstanceUID}}
		if instanceNumber != "" {
			obj["00200013"] = map[string]interface{}{"vr": "IS", "Value": []string{instanceNumber}}
		}
		if modality != "" {
			obj["00080060"] = map[string]interface{}{"vr": "CS", "Value": []string{modality}}
		}

		instances = append(instances, obj)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(instances); err != nil {
		log.Printf("handleDicomWebListInstances encode error: %v", err)
	}
}

// handleDicomWebSeriesMetadata returns a DICOM JSON array of instance
// metadata for a given StudyInstanceUID/SeriesInstanceUID.
//
// It reuses the study-level metadata from Google DICOMweb and filters
// to only the instances in the requested series, then returns them as-is
// (DICOM JSON datasets) so OHIF can build display sets.
func (h *Handlers) handleDicomWebSeriesMetadata(
	w http.ResponseWriter,
	r *http.Request,
	studyUID string,
	seriesUID string,
) {
	ctx := r.Context()

	// Fetch study-level metadata from Google Healthcare
	bytes, err := h.Dicom.StudyMetadataJSON(ctx, studyUID)
	if err != nil {
		log.Printf("handleDicomWebSeriesMetadata StudyMetadataJSON error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_metadata_error",
		})
		return
	}

	var datasets []map[string]interface{}
	if err := json.Unmarshal(bytes, &datasets); err != nil {
		log.Printf("handleDicomWebSeriesMetadata unmarshal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "invalid_dicom_metadata",
		})
		return
	}

	/////////////////////////////////////////
	//
	// 1) Determine dominant orientation for this SeriesInstanceUID.
	//
	//   Takes out any outliers based on orientation
	//    such as those different orientated slides from our OHIF test
	//    should all be same orientation after this
	//
	orientationCounts := make(map[string]int)
	for _, ds := range datasets {
		sUID := dicomwebTagString(ds, "0020000E") // SeriesInstanceUID
		if sUID != seriesUID {
			continue
		}
		key := dicomwebOrientationKey(ds)
		if key == "" {
			continue
		}
		orientationCounts[key]++
	}
	// Calculate the dominant orientation
	dominantOrientation := ""
	maxCount := 0
	for key, count := range orientationCounts {
		if count > maxCount {
			maxCount = count
			dominantOrientation = key
		}
	}
	//
	//
	////////////////////   END dominant orientation filtering  ////////////////////

	// Filter to only instances in the requested series
	filtered := make([]map[string]interface{}, 0, len(datasets))
	for _, ds := range datasets {
		sUID := dicomwebTagString(ds, "0020000E") // SeriesInstanceUID

		////////////////////////////////////////
		//
		//   Apply Dominant Orientation Filter
		//
		//    Should filter out any without the common orientation of
		//     the dominant set of slides, such as
		//     the first outliers we were seeing
		//
		if dominantOrientation != "" {
			key := dicomwebOrientationKey(ds)
			if key != "" && key != dominantOrientation {
				// Skip orientation outliers
				continue
			}
		}
		//
		/////////////////////////////////////////////

		if sUID == seriesUID {
			filtered = append(filtered, ds)
		}

	}

	// Return DICOM JSON; OHIF sends Accept: application/dicom+json
	w.Header().Set("Content-Type", "application/dicom+json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(filtered); err != nil {
		log.Printf("handleDicomWebSeriesMetadata encode error: %v", err)
	}
}

// handleDicomWebRetrieveInstance proxies a single DICOM instance retrieval
// (raw or rendered) from the configured DICOM store.
func (h *Handlers) handleDicomWebRetrieveInstance(w http.ResponseWriter, r *http.Request, studyUID, seriesUID, sopUID string, rendered bool) {
	if h.Dicom == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "dicom_client_not_configured",
		})
		return
	}

	ctx := r.Context()

	var resp *http.Response
	var err error
	if rendered {
		resp, err = h.Dicom.RetrieveRenderedInstanceJPEG(ctx, studyUID, seriesUID, sopUID)
	} else {
		resp, err = h.Dicom.RetrieveInstanceRaw(ctx, studyUID, seriesUID, sopUID)
	}
	if err != nil {
		log.Printf("handleDicomWebRetrieveInstance error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_retrieve_error",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Printf("handleDicomWebRetrieveInstance upstream status %d %s", resp.StatusCode, resp.Status)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// Pass through upstream Content-Type and (if present) Content-Length.
	for k, values := range resp.Header {
		// Avoid hop-by-hop headers like Transfer-Encoding; we mainly care about
		// Content-Type and Content-Length.
		if strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Content-Length") {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("handleDicomWebRetrieveInstance io.Copy error: %v", err)
	}
}

// //////////////////////////////////////////////////////////
//
//	Handle Dicom Web Retreive Frames
//
//	 Fixes issues with returning pure Dicom - this should return multipart
//	  for the individual frame calls, that were returning split images in OHIF
//
// handleDicomWebRetrieveFrames proxies a DICOMweb frames retrieval for a
// single instance. It returns pixel data only (application/octet-stream),
// not a full application/dicom instance.
func (h *Handlers) handleDicomWebRetrieveFrames(
	w http.ResponseWriter,
	r *http.Request,
	studyUID, seriesUID, sopUID, frameList string,
) {
	if h.Dicom == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"error": "dicom_client_not_configured",
		})
		return
	}

	ctx := r.Context()

	// Forward the Accept header so GCP returns what OHIF asked for.
	accept := r.Header.Get("Accept")

	resp, err := h.Dicom.RetrieveFramesRaw(ctx, studyUID, seriesUID, sopUID, frameList, accept)
	if err != nil {
		log.Printf("handleDicomWebRetrieveFrames error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": "dicom_frames_error",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Printf("handleDicomWebRetrieveFrames upstream status %d %s", resp.StatusCode, resp.Status)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// Pass through upstream Content-Type and Content-Length so boundary etc. are preserved.
	for k, values := range resp.Header {
		if strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Content-Length") {
			for _, v := range values {
				w.Header().Add(k, v)
			}
		}
	}

	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("handleDicomWebRetrieveFrames io.Copy error: %v", err)
	}
}
