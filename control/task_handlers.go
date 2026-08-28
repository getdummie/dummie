package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
	"control/internal/proto"
)

const taskExpireRetry = 30 * time.Second

const taskExpireAttempts = 240

const taskCleanupEvery = 24 * time.Hour

const taskCertRenewEvery = 24 * time.Hour

type vmTargetExpirePayload struct {
	VMID        string `json:"vm_id"`
	VMName      string `json:"vm_name"`
	Destination string `json:"destination"`
	TTLSeconds  int64  `json:"ttl_seconds"`
}

type vmExpirePayload struct {
	VMName     string `json:"vm_name"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

func handleVMTargetExpire(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	if !t.SubjectID.Valid {
		return taskFailed("this task names no destination to expire")
	}
	row, err := r.q.GetVMNetworkTargetForExpiry(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the destination had already been removed, so there was nothing to expire")
		}
		return taskRetry(taskExpireRetry, "could not read the destination: %v", err)
	}

	n, err := r.q.DeleteVMNetworkTargetByID(ctx, t.SubjectID)
	if err != nil {
		return taskRetry(taskExpireRetry, "could not remove the destination: %v", err)
	}

	pushSuricataRules(ctx, r.q, r.hub, row.ClientID)
	pushCoreDNSConfig(ctx, r.q, r.hub, row.ClientID)

	if n == 0 {
		return taskDone("the destination %s was already gone; the host's policy was regenerated anyway", row.Destination)
	}
	return taskDone("withdrew %s from %s", row.Destination, row.VMName)
}

func handleVMExpire(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	if !t.SubjectID.Valid {
		return taskFailed("this task names no vm to expire")
	}
	vm, err := r.q.GetVM(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the vm record had already been deleted, so there was nothing to expire")
		}
		return taskRetry(taskExpireRetry, "could not read the vm: %v", err)
	}

	switch vm.Status {
	case "gone":
		return taskDone("%s is gone", vm.Name)
	case "failed":
		return taskCancelled("%s never started, so it had nothing to destroy", vm.Name)
	case "pending":
		return taskRetry(taskExpireRetry, "the create is still in flight, so there is nothing to destroy yet")
	}
	if vm.VMID == "" {
		return taskRetry(taskExpireRetry, "the host has not reported an id for this vm yet")
	}

	clientID := uuid.UUID(vm.ClientID.Bytes).String()
	if !r.hub.Connected(clientID) {
		return taskRetry(taskExpireRetry, "the host running %s is not connected, so the destroy is waiting", vm.Name)
	}

	env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(vm.ID.Bytes).String(), proto.Job{
		Kind: proto.KindVMDestroy,
		VMID: vm.VMID,
	})
	if err != nil {
		return taskFailed("could not build the destroy job: %v", err)
	}
	if err := r.hub.Send(clientID, env); err != nil {
		return taskRetry(taskExpireRetry, "could not deliver the destroy to the host: %v", err)
	}
	log.Printf("task: asked host %s to destroy expired vm %s (%s)", clientID, vm.Name, vm.VMID)

	return taskRetry(taskExpireRetry, "asked the host to destroy %s; waiting for it to confirm", vm.Name)
}

func handleTaskCleanup(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	n, err := r.q.DeleteSettledScheduledTasksBefore(ctx, taskRetention.Seconds())
	if err != nil {
		return taskRetry(taskExpireRetry, "could not prune settled tasks: %v", err)
	}
	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskCleanup,
		Reason: "prune settled scheduled tasks older than the retention window",
		After:  taskCleanupEvery,
	}); err != nil {
		return taskRetry(taskExpireRetry, "pruned %d task(s) but could not schedule the next cleanup: %v", n, err)
	}
	return taskDone("pruned %d settled task(s) older than %d days", n, int(taskRetention.Hours()/24))
}

const (
	customDomainAttempts = 40

	customDomainRetry = 30 * time.Second

	// How long a host is given to finish an order before the job is sent
	// again. An HTTP-01 order is seconds of work; this is the window for a
	// host that took the job and then died with it.
	customDomainOrderTimeout = 5 * time.Minute
)

type customDomainIssuePayload struct {
	Renew bool `json:"renew,omitempty"`
}

// handleCustomDomainIssue drives one name from "the owner says the CNAME is
// there" to "a host holds a certificate for it". A renewal takes the same path
// but never writes the status: the old certificate keeps serving until the new
// one lands, and a renewal that cannot be finished is a failed task, not a
// domain taken off the air.
func handleCustomDomainIssue(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	if !t.SubjectID.Valid {
		return taskFailed("this task names no custom domain")
	}
	var payload customDomainIssuePayload
	_ = json.Unmarshal(t.Payload, &payload)

	row, err := r.q.GetCustomDomainForIssue(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the custom domain had already been removed")
		}
		return taskRetry(customDomainRetry, "could not read the custom domain: %v", err)
	}

	if payload.Renew {
		if row.CertNotAfter.Valid && time.Until(row.CertNotAfter.Time) > certRenewBefore {
			return taskDone("%s is serving a certificate that is good until %s",
				row.Domain, row.CertNotAfter.Time.Format(time.RFC3339))
		}
	} else {
		switch row.Status {
		case customDomainActive:
			return taskDone("%s is serving with its own certificate", row.Domain)
		case customDomainFailed:
			return taskFailed("%s", row.LastError)
		}
	}

	give := func(detail string) taskOutcome {
		if !payload.Renew {
			noteCustomDomain(ctx, r, row.ID, customDomainFailed, detail)
		}
		return taskFailed("%s", detail)
	}
	lastAttempt := t.Attempts+1 >= t.MaxAttempts

	if row.DomainTLD == "" {
		return give("the host this vm runs on has no domain to point at")
	}
	target := row.VMName + "." + row.DomainTLD

	if row.OrderedAt.Valid && time.Since(row.OrderedAt.Time) < customDomainOrderTimeout {
		if lastAttempt {
			return give("the host never came back with a certificate for " + row.Domain)
		}
		return taskRetry(customDomainRetry, "the host is obtaining a certificate for %s", row.Domain)
	}

	if err := verifyCNAME(ctx, r.q, row.Domain, target); err != nil {
		if lastAttempt {
			return give(err.Error())
		}
		if !payload.Renew {
			noteCustomDomain(ctx, r, row.ID, customDomainVerifying, err.Error())
		}
		return taskRetry(customDomainRetry, "%v", err)
	}

	clientID := uuid.UUID(row.ClientID.Bytes).String()
	if !r.hub.Connected(clientID) {
		if lastAttempt {
			return give("the host serving this vm never reconnected")
		}
		return taskRetry(customDomainRetry, "the host serving %s is not connected", row.VMName)
	}

	env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(row.ID.Bytes).String(), proto.Job{
		Kind: proto.KindCustomCert,
		CustomCert: &proto.CustomCertOrder{
			Domain:    row.Domain,
			Email:     row.AcmeEmail,
			Directory: row.AcmeDirectory,
		},
	})
	if err != nil {
		return taskFailed("could not build the certificate job: %v", err)
	}
	if err := r.hub.Send(clientID, env); err != nil {
		return taskRetry(customDomainRetry, "could not deliver the certificate job to the host: %v", err)
	}
	if !payload.Renew {
		noteCustomDomain(ctx, r, row.ID, customDomainIssuing, "")
	}
	if err := r.q.MarkCustomDomainOrdered(ctx, row.ID); err != nil {
		log.Printf("could not record that %s was ordered: %v", row.Domain, err)
	}
	log.Printf("task: asked host %s to obtain a certificate for %s", clientID, row.Domain)

	return taskRetry(customDomainRetry, "asked the host to obtain a certificate for %s", row.Domain)
}

func noteCustomDomain(ctx context.Context, r *taskRunner, id pgtype.UUID, status, detail string) {
	if _, err := r.q.SetCustomDomainStatus(ctx, db.SetCustomDomainStatusParams{
		ID: id, Status: status, LastError: detail,
	}); err != nil {
		log.Printf("could not record the custom domain status: %v", err)
	}
}

// verifyCNAME insists on a real CNAME. An A record that happens to hold the
// right address is not accepted: the CNAME is what keeps the name following
// this VM if it is ever rebuilt on another host with another address.
func verifyCNAME(ctx context.Context, q *db.Queries, domain, target string) error {
	lookupCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()

	cname, err := configuredResolver(lookupCtx, q).LookupCNAME(lookupCtx, domain)
	if err != nil {
		return fmt.Errorf("%s does not resolve yet", domain)
	}
	got := strings.TrimSuffix(strings.ToLower(cname), ".")
	if got == strings.ToLower(domain) {
		return fmt.Errorf("%s has no CNAME record yet; point it at %s", domain, target)
	}
	if got != strings.ToLower(target) {
		return fmt.Errorf("%s is a CNAME for %s, not %s", domain, got, target)
	}
	return nil
}

func handleCertRenew(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	started, skipped := 0, 0
	if r.certs != nil {
		due, err := r.q.ListDomainsDueForRenewal(ctx, certRenewBefore.Seconds())
		if err != nil {
			return taskRetry(taskExpireRetry, "could not list the domains due for renewal: %v", err)
		}
		for _, domain := range due {
			if err := r.certs.Start(domain); err != nil {
				log.Printf("not renewing %s: %v", domain.TLD, err)
				skipped++
				continue
			}
			started++
		}
	}

	custom, err := r.q.ListCustomDomainsDueForRenewal(ctx, certRenewBefore.Seconds())
	if err != nil {
		return taskRetry(taskExpireRetry, "could not list the custom domains due for renewal: %v", err)
	}
	for _, row := range custom {
		if err := startCustomDomainIssue(ctx, r.q, row, true); err != nil {
			log.Printf("not renewing %s: %v", row.Domain, err)
			skipped++
			continue
		}
		started++
	}

	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskCertRenew,
		Reason: "replace certificates that are close to expiring",
		After:  taskCertRenewEvery,
	}); err != nil {
		return taskRetry(taskExpireRetry, "started %d renewal(s) but could not schedule the next sweep: %v", started, err)
	}
	if skipped > 0 {
		return taskDone("started %d certificate renewal(s), skipped %d already in progress", started, skipped)
	}
	return taskDone("started %d certificate renewal(s)", started)
}
