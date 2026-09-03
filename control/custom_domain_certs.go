package main

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
)

// vaultCustomCert records the certificate now serving a custom domain as the
// durable copy for that name. The vault owns the blobs: a binding only points
// at them, so deleting a vm leaves the certificate where it is and the next vm
// that claims the name can pick it up.
func vaultCustomCert(ctx context.Context, q *db.Queries, blobs *blobStore,
	domain string, owner pgtype.UUID, certKey, keyKey, fingerprint string, notAfter time.Time) {
	previous, err := q.GetCustomDomainCert(ctx, domain)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("could not read the stored certificate for %s: %v", domain, err)
		return
	}
	hadPrevious := err == nil

	if _, err := q.UpsertCustomDomainCert(ctx, db.UpsertCustomDomainCertParams{
		Domain:          domain,
		OwnerID:         owner,
		CertObjectKey:   certKey,
		KeyObjectKey:    keyKey,
		CertFingerprint: fingerprint,
		CertNotAfter:    pgtype.Timestamptz{Time: notAfter, Valid: true},
	}); err != nil {
		log.Printf("could not store the certificate for %s: %v", domain, err)
		return
	}

	if hadPrevious && blobs != nil && previous.CertObjectKey != certKey {
		_ = blobs.Delete(ctx, previous.CertObjectKey)
		_ = blobs.Delete(ctx, previous.KeyObjectKey)
	}
}

// reusableCustomCert answers with the stored certificate for a name when the
// same user is entitled to it and it has not expired. A certificate obtained by
// someone else is never handed over: whoever claims the name next has to prove
// control of it again.
func reusableCustomCert(ctx context.Context, q *db.Queries, domain string, owner pgtype.UUID) (db.CustomDomainCert, bool) {
	if !owner.Valid {
		return db.CustomDomainCert{}, false
	}
	row, err := q.GetCustomDomainCert(ctx, domain)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("could not read the stored certificate for %s: %v", domain, err)
		}
		return db.CustomDomainCert{}, false
	}
	if !row.OwnerID.Valid || row.OwnerID.Bytes != owner.Bytes {
		return db.CustomDomainCert{}, false
	}
	if !row.CertNotAfter.Valid || !row.CertNotAfter.Time.After(time.Now()) {
		return db.CustomDomainCert{}, false
	}
	return row, true
}
