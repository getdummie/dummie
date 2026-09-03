package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v5"

	"control/internal/db"
)

const (
	customDomainPendingDNS = "pending_dns"
	customDomainVerifying  = "verifying"
	customDomainIssuing    = "issuing"
	customDomainActive     = "active"
	customDomainFailed     = "failed"
)

const customDomainMaxLen = 253

type customDomainDTO struct {
	Domain string `json:"domain"`
	Status string `json:"status"`

	// The record the owner has to create at their registrar, and what it has
	// to point at. Both are echoed back at every status so the page can keep
	// showing the instructions while an order runs.
	CNAMEName   string `json:"cname_name"`
	CNAMETarget string `json:"cname_target"`

	LastError    string     `json:"last_error,omitempty"`
	URL          string     `json:"url,omitempty"`
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`

	// Set when this claim was served by a certificate already held for the
	// name, so the page can say the CNAME is the only thing left to do.
	CertReused bool `json:"cert_reused,omitempty"`
}

func (h *UserHandler) customDomainDTO(ctx context.Context, vm db.Vm, row db.VmCustomDomain) customDomainDTO {
	out := customDomainDTO{
		Domain:      row.Domain,
		Status:      row.Status,
		CNAMEName:   row.Domain,
		CNAMETarget: h.customDomainTarget(ctx, vm),
		LastError:   row.LastError,
	}
	if row.CertNotAfter.Valid {
		t := row.CertNotAfter.Time
		out.CertNotAfter = &t
	}
	if row.Status == customDomainActive {
		scheme := "http"
		if h.prod {
			scheme = "https"
		}
		out.URL = scheme + "://" + row.Domain
	}
	return out
}

func (h *UserHandler) customDomainTarget(ctx context.Context, vm db.Vm) string {
	tld := h.vmDomainTLD(ctx, vm)
	if tld == "" {
		return ""
	}
	return vm.Name + "." + tld
}

// @Summary     Read this VM's custom domain
// @Description The domain a VM answers to besides its own name under the fleet domain, and how far along its certificate is. 404 means none has been requested.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     200 {object} customDomainDTO
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/domain [get]
func (h *UserHandler) GetCustomDomain(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	row, err := h.q.GetCustomDomainByVM(ctx, vm.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "this vm has no custom domain")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the custom domain")
	}
	return c.JSON(http.StatusOK, h.customDomainDTO(ctx, vm, row))
}

type customDomainReq struct {
	Domain string `json:"domain"`
}

// @Summary     Request a custom domain for this VM
// @Description Records the name and answers with the CNAME to create at your registrar. Nothing is verified and no certificate is asked for until you confirm the record exists. A VM holds one custom domain, so this replaces any earlier one. If you already obtained a certificate for this name and it has not expired, it is reused: the domain comes back active at once and pointing the CNAME at the new VM is all that is left to do.
// @Tags        vms
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id   path string true "vm id" format(uuid)
// @Param       body body customDomainReq true "domain"
// @Success     201 {object} customDomainDTO
// @Failure     400 {object} apiError
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "the host has no domain, or the name is already claimed by another vm"
// @Router      /vms/{id}/domain [post]
func (h *UserHandler) SetCustomDomain(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	var req customDomainReq
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	ctx := c.Request().Context()
	tld := h.vmDomainTLD(ctx, vm)
	if tld == "" {
		return echo.NewHTTPError(http.StatusConflict, "the host this vm runs on has no domain, so it publishes nothing to point at")
	}
	domain, err := normalizeCustomDomain(req.Domain, tld)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if existing, err := h.q.GetCustomDomainByVM(ctx, vm.ID); err == nil {
		if existing.Domain == domain {
			return c.JSON(http.StatusCreated, h.customDomainDTO(ctx, vm, existing))
		}
		if err := h.dropCustomDomain(ctx, existing, vm.ClientID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "could not replace the previous custom domain")
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the custom domain")
	}

	row, err := h.q.CreateCustomDomain(ctx, db.CreateCustomDomainParams{VMID: vm.ID, Domain: domain})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return echo.NewHTTPError(http.StatusConflict, "that domain is already claimed by another vm")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not record the custom domain")
	}

	row, reused := h.adoptStoredCert(ctx, vm, row)
	dto := h.customDomainDTO(ctx, vm, row)
	dto.CertReused = reused
	return c.JSON(http.StatusCreated, dto)
}

// @Summary     Confirm the CNAME and start the certificate
// @Description Call this once the CNAME exists. The server checks it resolves to this VM's name under the fleet domain and then has the host obtain a certificate over HTTP-01. Both happen in the background: poll GET /vms/{id}/domain until the status is active or failed.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     202 {object} customDomainDTO
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Failure     409 {object} apiError "a check is already running"
// @Router      /vms/{id}/domain/verify [post]
func (h *UserHandler) VerifyCustomDomain(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	row, err := h.q.GetCustomDomainByVM(ctx, vm.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, "this vm has no custom domain")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the custom domain")
	}
	if row.Status == customDomainVerifying || row.Status == customDomainIssuing {
		return echo.NewHTTPError(http.StatusConflict, "a check is already running for this domain")
	}

	updated, err := h.q.SetCustomDomainStatus(ctx, db.SetCustomDomainStatusParams{
		ID: row.ID, Status: customDomainVerifying, LastError: "",
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not start the check")
	}
	if err := startCustomDomainIssue(ctx, h.q, updated, false); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not schedule the check")
	}
	return c.JSON(http.StatusAccepted, h.customDomainDTO(ctx, vm, updated))
}

// @Summary     Drop this VM's custom domain
// @Description Stops serving the name, forgets its certificate and cancels any order in flight. The CNAME at your registrar is yours to remove.
// @Tags        vms
// @Produce     json
// @Security    BearerAuth
// @Param       id path string true "vm id" format(uuid)
// @Success     204 "removed"
// @Failure     401 {object} apiError
// @Failure     404 {object} apiError
// @Router      /vms/{id}/domain [delete]
func (h *UserHandler) DeleteCustomDomain(c *echo.Context) error {
	vm, err := h.ownedVM(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	row, err := h.q.GetCustomDomainByVM(ctx, vm.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return c.NoContent(http.StatusNoContent)
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "could not read the custom domain")
	}
	if err := h.dropCustomDomain(ctx, row, vm.ClientID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "could not remove the custom domain")
	}
	return c.NoContent(http.StatusNoContent)
}

// dropCustomDomain stops serving a name. The certificate is left in the vault:
// it belongs to the name and the user, not to the vm that happened to be
// answering for it, and the expiry sweep is what eventually reclaims it.
func (h *UserHandler) dropCustomDomain(ctx context.Context, row db.VmCustomDomain, clientID pgtype.UUID) error {
	if err := h.q.DeleteCustomDomain(ctx, row.ID); err != nil {
		return err
	}
	cancelTasksForSubject(ctx, h.q, subjectCustomDomain, row.ID, "the custom domain was removed")
	pushDpipeConfig(ctx, h.q, h.blobs, h.hub, clientID)
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, clientID)
	return nil
}

// adoptStoredCert points a fresh claim at the certificate already held for the
// name, sparing the owner an ACME round trip when they rebuild a vm. The name
// goes live straight away; the CNAME still has to be moved to the new vm before
// anything reaches it, which is what the caller tells them.
func (h *UserHandler) adoptStoredCert(ctx context.Context, vm db.Vm, row db.VmCustomDomain) (db.VmCustomDomain, bool) {
	stored, ok := reusableCustomCert(ctx, h.q, row.Domain, vm.CreatedBy)
	if !ok {
		return row, false
	}
	updated, err := h.q.ReuseCustomDomainCert(ctx, db.ReuseCustomDomainCertParams{
		ID:              row.ID,
		CertObjectKey:   stored.CertObjectKey,
		KeyObjectKey:    stored.KeyObjectKey,
		CertFingerprint: stored.CertFingerprint,
		CertNotAfter:    stored.CertNotAfter,
		CertIssuedAt:    stored.CertIssuedAt,
	})
	if err != nil {
		log.Printf("could not reuse the stored certificate for %s: %v", row.Domain, err)
		return row, false
	}
	log.Printf("reused the stored certificate for %s, valid until %s",
		row.Domain, stored.CertNotAfter.Time.Format(time.RFC3339))
	pushDpipeConfig(ctx, h.q, h.blobs, h.hub, vm.ClientID)
	pushProxyConfig(ctx, h.q, h.hub, h.proxy, vm.ClientID)
	return updated, true
}

func normalizeCustomDomain(raw, tld string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", errors.New("a domain is required")
	}
	if len(domain) > customDomainMaxLen {
		return "", fmt.Errorf("a domain may be at most %d characters", customDomainMaxLen)
	}
	if !hostnamePattern.MatchString(domain) || !strings.Contains(domain, ".") {
		return "", errors.New("that is not a domain name")
	}
	if domain == tld || strings.HasSuffix(domain, "."+tld) {
		return "", fmt.Errorf("%s is already served under %s, so it does not need a custom domain", domain, tld)
	}
	return domain, nil
}

func startCustomDomainIssue(ctx context.Context, q *db.Queries, row db.VmCustomDomain, renew bool) error {
	cancelTasksForSubject(ctx, q, subjectCustomDomain, row.ID, "superseded by a new check")
	reason := "verify the cname for " + row.Domain + " and obtain a certificate for it"
	if renew {
		reason = "replace the certificate for " + row.Domain + " before it expires"
	}
	_, err := scheduleTask(ctx, q, scheduleTaskParams{
		Kind:        taskCustomDomainIssue,
		SubjectKind: subjectCustomDomain,
		SubjectID:   row.ID,
		Payload:     customDomainIssuePayload{Renew: renew},
		Reason:      reason,
		MaxAttempts: customDomainAttempts,
	})
	return err
}

// storeCustomCert records a certificate a host obtained for a custom domain and
// sends it straight back out to that host, which is holding nothing until this
// lands: the order ran there but the copy that survives a reinstall is ours.
func storeCustomCert(ctx context.Context, q *db.Queries, blobs *blobStore, hub *Hub, proxy proxyAuthConfig,
	row db.VmCustomDomain, clientID pgtype.UUID, certPEM, keyPEM string) error {
	if blobs == nil {
		return errNoBlobStore
	}
	info, err := validateCertificateFor(certPEM, keyPEM, row.Domain)
	if err != nil {
		return err
	}

	certKey := blobs.newKey("certs", "fullchain.pem")
	keyKey := blobs.newKey("certs", "privkey.pem")
	if err := blobs.Put(ctx, certKey, "application/x-pem-file", strings.NewReader(certPEM)); err != nil {
		return fmt.Errorf("could not store the certificate: %w", err)
	}
	if err := blobs.Put(ctx, keyKey, "application/x-pem-file", strings.NewReader(keyPEM)); err != nil {
		_ = blobs.Delete(ctx, certKey)
		return errors.New("could not store the private key")
	}

	if _, err := q.UpdateCustomDomainCert(ctx, db.UpdateCustomDomainCertParams{
		ID:              row.ID,
		CertObjectKey:   certKey,
		KeyObjectKey:    keyKey,
		CertFingerprint: info.Fingerprint,
		CertNotAfter:    pgtype.Timestamptz{Time: info.NotAfter, Valid: true},
	}); err != nil {
		_ = blobs.Delete(ctx, certKey)
		_ = blobs.Delete(ctx, keyKey)
		return fmt.Errorf("could not record the certificate: %w", err)
	}

	owner, err := q.GetCustomDomainOwner(ctx, row.ID)
	if err != nil {
		log.Printf("could not read the owner of %s: %v", row.Domain, err)
	}
	vaultCustomCert(ctx, q, blobs, row.Domain, owner, certKey, keyKey, info.Fingerprint, info.NotAfter)

	log.Printf("stored a certificate for the custom domain %s, valid until %s",
		row.Domain, info.NotAfter.Format(time.RFC3339))
	pushDpipeConfig(ctx, q, blobs, hub, clientID)
	pushProxyConfig(ctx, q, hub, proxy, clientID)
	return nil
}
