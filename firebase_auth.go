package main

import (
	"context"
	"log"
	"sync"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
)

// FirebaseVerifier lazily initializes a Firebase Admin Auth client
// using Application Default Credentials, similar to firebase_auth.py.
// It is safe for concurrent use.
type FirebaseVerifier struct {
	client *auth.Client
}

var (
	fvOnce sync.Once
	fv     *FirebaseVerifier
	fvErr  error
)

// getFirebaseVerifier initializes (once) and returns a FirebaseVerifier.
// It uses ADC and the provided project ID, matching the Python behavior
// of relying on GOOGLE_APPLICATION_CREDENTIALS / gcloud auth.
func getFirebaseVerifier(ctx context.Context, projectID string) (*FirebaseVerifier, error) {
	fvOnce.Do(func() {
		app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
		if err != nil {
			fvErr = err
			log.Printf("firebase.NewApp error: %v", err)
			return
		}

		client, err := app.Auth(ctx)
		if err != nil {
			fvErr = err
			log.Printf("firebase app.Auth error: %v", err)
			return
		}

		fv = &FirebaseVerifier{client: client}
	})

	return fv, fvErr
}

// verifyIDToken verifies a Firebase ID token and returns the decoded token.
// If verification fails, it returns (nil, error) but does *not* log; callers
// can decide whether to fall back to dev bearer behavior.
func (h *Handlers) verifyIDToken(ctx context.Context, idToken string) (*auth.Token, error) {
	verifier, err := getFirebaseVerifier(ctx, h.Cfg.ProjectID)
	if err != nil || verifier == nil {
		return nil, err
	}
	return verifier.client.VerifyIDToken(ctx, idToken)
}
