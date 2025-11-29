package main

import (
	"cloud.google.com/go/firestore"
	"context"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreDB wraps a Firestore client and exposes the small subset of
// operations we need for auth/accounts, mirroring htmlery_rest/db.py.
type FirestoreDB struct {
	client *firestore.Client
}

// NewFirestoreDB creates a new Firestore client for the given project ID.
func NewFirestoreDB(ctx context.Context, projectID string) (*FirestoreDB, error) {
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("firestore.NewClient: %w", err)
	}
	return &FirestoreDB{client: client}, nil
}

// Close releases underlying Firestore resources.
func (db *FirestoreDB) Close() error {
	return db.client.Close()
}

// Account corresponds to the Firestore document stored in the
// "accounts" collection by the Python service, extended for VisitVizor.
//
// Field tags ensure we keep the same document shape so existing data
// created by htmlery-rest remains compatible.
type Account struct {
	UserID        string  `firestore:"user_id" json:"user_id"`
	FirstName     string  `firestore:"first_name" json:"first_name"`
	LastName      string  `firestore:"last_name" json:"last_name"`
	BusinessName  string  `firestore:"business_name" json:"business_name"`
	Email         string  `firestore:"email" json:"email"`
	Phone         string  `firestore:"phone" json:"phone"`
	Authenticated bool    `firestore:"authenticated" json:"authenticated"`
	LastLogin     *string `firestore:"last_login" json:"last_login"`
	CreatedAt     string  `firestore:"created_at" json:"created_at"`
}

// CreateAccount mirrors _FS.create_account in db.py.
func (db *FirestoreDB) CreateAccount(ctx context.Context, userID string, acc *Account) error {
	if acc == nil {
		return fmt.Errorf("nil account")
	}
	// Ensure the user_id field matches the document key
	acc.UserID = userID
	// Use a full document write; since we always send the full Account
	// this is effectively the same as the Python set(..., merge=True)
	// for our use case.
	_, err := db.client.Collection("accounts").Doc(userID).Set(ctx, acc)
	if err != nil {
		return fmt.Errorf("create account (%s): %w", userID, err)
	}
	return nil
}

// GetAccount mirrors _FS.get_account in db.py.
func (db *FirestoreDB) GetAccount(ctx context.Context, userID string) (*Account, error) {
	snap, err := db.client.Collection("accounts").Doc(userID).Get(ctx)
	if err != nil {
		// NotFound is not considered an error for our caller; they should
		// interpret nil account as "no such account".
		// We detect this via the gRPC status code instead of firestore.IsNotFound.
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("get account (%s): %w", userID, err)
	}
	var acc Account
	if err := snap.DataTo(&acc); err != nil {
		return nil, fmt.Errorf("decode account (%s): %w", userID, err)
	}
	return &acc, nil
}

// DeleteAccount mirrors _FS.delete_account in db.py.
func (db *FirestoreDB) DeleteAccount(ctx context.Context, userID string) error {
	_, err := db.client.Collection("accounts").Doc(userID).Delete(ctx)
	if err != nil {
		return fmt.Errorf("delete account (%s): %w", userID, err)
	}
	return nil
}

// UpdateAccount performs a partial update (merge) with the provided fields.
// This is used for the patient profile page where they can edit their info.
func (db *FirestoreDB) UpdateAccount(ctx context.Context, userID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	_, err := db.client.Collection("accounts").Doc(userID).Set(ctx, updates, firestore.MergeAll)
	if err != nil {
		return fmt.Errorf("update account (%s): %w", userID, err)
	}
	return nil
}
