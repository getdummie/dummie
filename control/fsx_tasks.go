package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"control/internal/db"
	"control/internal/proto"
)

const (
	taskVMDiskFSx = "vm.disk_fsx"
	taskFSxGC     = "fsx.gc"

	taskVMDiskFSxTimeout = 45 * time.Minute
	vmDiskFSxAttempts    = 5
	vmDiskFSxRetry       = 30 * time.Second

	// Building a rootfs is a download and an mkfs; seeding is a multi-gigabyte
	// copy over NFS.
	fsxHostJobWait = 30 * time.Minute

	taskFSxGCEvery = 10 * time.Minute
)

var fsxRootfsName = regexp.MustCompile(`^[0-9a-f]{32}-rootfs\.ext4$`)

// handleVMDiskFSx gives a pending fsx vm its volume and then hands the create to
// its host. Every step is resumable, so a retry picks up where the last one died.
func handleVMDiskFSx(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	vm, err := r.q.GetVM(ctx, t.SubjectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskCancelled("the vm was deleted before its disk was made")
		}
		return taskRetry(vmDiskFSxRetry, "could not read the vm: %v", err)
	}
	if vm.Status != "pending" {
		return taskCancelled("%s is %s, no longer waiting for a disk", vm.Name, vm.Status)
	}
	fail := func(format string, args ...any) taskOutcome {
		msg := fmt.Sprintf(format, args...)
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := r.q.MarkVMFailed(failCtx, db.MarkVMFailedParams{ID: vm.ID, LastError: msg}); err != nil {
			log.Printf("could not mark vm %s failed: %v", vm.Name, err)
		}
		return taskFailed("%s", msg)
	}
	retryOrFail := func(format string, args ...any) taskOutcome {
		if t.Attempts >= t.MaxAttempts {
			return fail(format, args...)
		}
		return taskRetry(vmDiskFSxRetry, format, args...)
	}

	store, err := openFSx(ctx, r.q)
	if err != nil {
		return retryOrFail("%v", err)
	}
	var spec proto.VMSpec
	if err := json.Unmarshal(vm.Spec, &spec); err != nil {
		return fail("the vm's spec is unreadable: %v", err)
	}
	// The link recorded at create may have expired by a retry.
	if r.blobs != nil && vm.OSImageID.Valid {
		osImage, err := r.q.GetOSImage(ctx, vm.OSImageID)
		if err != nil {
			return retryOrFail("could not read the os image: %v", err)
		}
		if spec.RootfsTar, err = r.blobs.PresignGet(ctx, osImage.ObjectKey, osImage.FileName); err != nil {
			return retryOrFail("could not prepare the os image download: %v", err)
		}
	}
	clientID := uuid.UUID(vm.ClientID.Bytes).String()
	if !r.hub.Connected(clientID) {
		return retryOrFail("the host for %s is not connected", vm.Name)
	}

	res, err := r.hub.Ask(ctx, clientID, proto.Job{Kind: proto.KindDiskPrepare, VM: &spec}, fsxHostJobWait)
	if err != nil {
		return retryOrFail("could not ask the host to build the rootfs: %v", err)
	}
	if !res.OK || res.Rootfs == nil {
		return fail("the host could not build the rootfs: %s", res.Error)
	}
	if !fsxRootfsName.MatchString(res.Rootfs.Name) {
		return fail("the host named its rootfs %q, which is not a built rootfs", res.Rootfs.Name)
	}

	img, out := ensureFSxImage(ctx, r, store, t, vm, clientID, *res.Rootfs)
	if out != nil {
		if out.status == "" && t.Attempts >= t.MaxAttempts {
			return fail("%s", out.detail)
		}
		return *out
	}

	disk, err := r.q.GetFSxDiskByVM(ctx, vm.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		size := max(int64(vm.DiskMiB)<<20, img.SizeBytes)
		vmID := uuid.UUID(vm.ID.Bytes).String()
		vol, cerr := store.createVolume(ctx, vmID, "vm-"+vmID, size, img.SnapshotARN)
		if cerr == nil {
			vol, cerr = store.waitVolume(ctx, aws.ToString(vol.VolumeId))
		}
		if cerr != nil {
			return retryOrFail("%v", cerr)
		}
		disk, err = r.q.CreateFSxDisk(ctx, db.CreateFSxDiskParams{
			Vm:         vm.ID,
			Image:      img.ID,
			VolumeID:   aws.ToString(vol.VolumeId),
			VolumePath: volumePath(vol),
		})
	}
	if err != nil {
		return retryOrFail("could not record the disk volume: %v", err)
	}

	fsx := store.disk(disk.VolumePath)
	spec.FSx = &fsx
	env, err := proto.NewEnvelope(proto.TypeJob, uuid.UUID(vm.ID.Bytes).String(), proto.Job{
		Kind: proto.KindVMCreate,
		VM:   &spec,
	})
	if err != nil {
		return fail("could not build the create job: %v", err)
	}
	if err := r.hub.Send(clientID, env); err != nil {
		return retryOrFail("could not deliver the create to the host: %v", err)
	}
	return taskDone("cloned %s for %s and handed the create to its host", disk.VolumeID, vm.Name)
}

// ensureFSxImage returns the ready image for a rootfs, building it on this host
// if nobody has. A nil outcome means the image is ready to clone.
func ensureFSxImage(ctx context.Context, r *taskRunner, store *fsxStore, t db.ScheduledTask, vm db.Vm, clientID string, rootfs proto.PreparedRootfs) (db.FsxImage, *taskOutcome) {
	retry := func(format string, args ...any) (db.FsxImage, *taskOutcome) {
		out := taskRetry(vmDiskFSxRetry, format, args...)
		return db.FsxImage{}, &out
	}

	// Another vm's task may be building the same image; waiting here rather than
	// retrying keeps that wait from spending this task's few attempts.
	var img db.FsxImage
	for {
		got, err := r.q.GetFSxImageByRootfs(ctx, rootfs.Name)
		if err == nil && got.Status == "ready" {
			return got, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return retry("could not read the image for %s: %v", rootfs.Name, err)
		}
		img, err = r.q.ClaimFSxImage(ctx, db.ClaimFSxImageParams{
			Rootfs:    rootfs.Name,
			OSImageID: vm.OSImageID,
			SizeBytes: rootfs.SizeBytes,
			ClaimedBy: t.ID,
		})
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return retry("could not claim the image for %s: %v", rootfs.Name, err)
		}
		if err := sleepCtx(ctx, fsxPoll); err != nil {
			return retry("gave up waiting for another task to build the image for %s", rootfs.Name)
		}
	}
	token := uuid.UUID(img.ID.Bytes).String()

	if img.VolumeID == "" {
		vol, err := store.createVolume(ctx, token, "img-"+rootfs.Name[:32], rootfs.SizeBytes, "")
		if err == nil {
			vol, err = store.waitVolume(ctx, aws.ToString(vol.VolumeId))
		}
		if err != nil {
			return retry("%v", err)
		}
		img.VolumeID, img.VolumePath = aws.ToString(vol.VolumeId), volumePath(vol)
		if err := r.q.SetFSxImageVolume(ctx, db.SetFSxImageVolumeParams{
			ID: img.ID, VolumeID: img.VolumeID, VolumePath: img.VolumePath,
		}); err != nil {
			return retry("could not record the image volume: %v", err)
		}
	}

	if img.SnapshotID == "" {
		res, err := r.hub.Ask(ctx, clientID, proto.Job{
			Kind: proto.KindDiskSeed,
			Seed: &proto.DiskSeed{Rootfs: rootfs.Name, FSx: store.disk(img.VolumePath)},
		}, fsxHostJobWait)
		if err != nil {
			return retry("could not ask the host to seed the image: %v", err)
		}
		if !res.OK {
			return retry("the host could not seed the image: %s", res.Error)
		}
		snap, err := store.createSnapshot(ctx, token+"-snap", "golden", img.VolumeID)
		if err != nil {
			return retry("%v", err)
		}
		img.SnapshotID, img.SnapshotARN = aws.ToString(snap.SnapshotId), aws.ToString(snap.ResourceARN)
		if err := r.q.SetFSxImageSnapshot(ctx, db.SetFSxImageSnapshotParams{
			ID: img.ID, SnapshotID: img.SnapshotID, SnapshotARN: img.SnapshotARN,
		}); err != nil {
			return retry("could not record the image snapshot: %v", err)
		}
	}
	if err := store.waitSnapshot(ctx, img.SnapshotID); err != nil {
		return retry("%v", err)
	}
	if err := r.q.MarkFSxImageReady(ctx, img.ID); err != nil {
		return retry("could not mark the image ready: %v", err)
	}
	log.Printf("task: fsx image %s ready as %s (%s)", rootfs.Name, img.VolumeID, img.SnapshotID)
	return img, nil
}

// handleFSxGC deletes the volumes of vms that are gone and the images nothing
// is cloned from any more. FSx deletes asynchronously, so the rows go as soon
// as the delete is accepted.
func handleFSxGC(ctx context.Context, r *taskRunner, t db.ScheduledTask) taskOutcome {
	next := func(out taskOutcome) taskOutcome {
		if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
			Kind:   taskFSxGC,
			Reason: "delete fsx volumes no vm or image needs",
			After:  taskFSxGCEvery,
		}); err != nil {
			return taskRetry(taskFSxGCEvery, "could not schedule the next sweep: %v", err)
		}
		return out
	}

	disks, err := r.q.ListReleasedFSxDisks(ctx)
	if err != nil {
		return next(taskDone("could not list released disks: %v", err))
	}
	images, err := r.q.ListUnusedFSxImages(ctx)
	if err != nil {
		return next(taskDone("could not list unused images: %v", err))
	}
	if len(disks) == 0 && len(images) == 0 {
		return next(taskDone("nothing to delete"))
	}
	store, err := openFSx(ctx, r.q)
	if err != nil {
		return next(taskDone("%d disk(s) and %d image(s) wait on FSx: %v", len(disks), len(images), err))
	}

	var deleted, failed int
	for _, d := range disks {
		if err := store.deleteVolume(ctx, d.VolumeID); err != nil {
			log.Printf("fsx gc: %v", err)
			failed++
			continue
		}
		if err := r.q.DeleteFSxDisk(ctx, d.ID); err != nil {
			log.Printf("fsx gc: could not forget disk %s: %v", d.VolumeID, err)
			failed++
			continue
		}
		deleted++
	}
	for _, img := range images {
		if img.VolumeID != "" {
			if err := store.deleteVolume(ctx, img.VolumeID); err != nil {
				log.Printf("fsx gc: %v", err)
				failed++
				continue
			}
		}
		if err := r.q.DeleteFSxImage(ctx, img.ID); err != nil {
			log.Printf("fsx gc: could not forget image %s: %v", img.Rootfs, err)
			failed++
			continue
		}
		deleted++
	}
	return next(taskDone("deleted %d volume(s); %d left for the next sweep", deleted, failed))
}
