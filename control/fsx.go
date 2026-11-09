package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/fsx"
	"github.com/aws/aws-sdk-go-v2/service/fsx/types"

	"control/internal/db"
	"control/internal/proto"
)

const (
	diskBackendLocal = "local"
	diskBackendFSx   = "fsx"

	// Small records suit the random reads and writes of a vm disk; the 128 KiB
	// default amplifies every 4 KiB guest write.
	fsxRecordSizeKiB = 32

	fsxPoll = 5 * time.Second
)

var errFSxNotConfigured = errors.New("FSx for OpenZFS is not configured; an admin has to set the file system in settings")

var fsxExportOptions = []string{"rw", "no_root_squash", "crossmnt", "sync"}

type fsxStore struct {
	client  *fsx.Client
	dns     string
	parent  string
	clients string
}

// openFSx reads the admin's FSx settings and looks the file system up. AWS
// credentials come from the default chain, so control runs under an instance
// role rather than holding keys.
func openFSx(ctx context.Context, q *db.Queries) (*fsxStore, error) {
	fsID := strings.TrimSpace(setting(ctx, q, settingFSxFileSystemID))
	clients := strings.TrimSpace(setting(ctx, q, settingFSxNFSClients))
	if fsID == "" || clients == "" {
		return nil, errFSxNotConfigured
	}
	var opts []func(*awsconfig.LoadOptions) error
	if region := strings.TrimSpace(setting(ctx, q, settingFSxRegion)); region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("could not load AWS credentials: %w", err)
	}
	client := fsx.NewFromConfig(cfg)

	out, err := client.DescribeFileSystems(ctx, &fsx.DescribeFileSystemsInput{FileSystemIds: []string{fsID}})
	if err != nil {
		return nil, fmt.Errorf("could not describe %s: %w", fsID, err)
	}
	if len(out.FileSystems) != 1 {
		return nil, fmt.Errorf("%s was not found", fsID)
	}
	fs := out.FileSystems[0]
	if fs.FileSystemType != types.FileSystemTypeOpenzfs || fs.OpenZFSConfiguration == nil {
		return nil, fmt.Errorf("%s is not an FSx for OpenZFS file system", fsID)
	}
	parent := strings.TrimSpace(setting(ctx, q, settingFSxParentVolumeID))
	if parent == "" {
		parent = aws.ToString(fs.OpenZFSConfiguration.RootVolumeId)
	}
	return &fsxStore{client: client, dns: aws.ToString(fs.DNSName), parent: parent, clients: clients}, nil
}

func (s *fsxStore) disk(path string) proto.FSxDisk {
	return proto.FSxDisk{Server: s.dns, Path: path}
}

func volumePath(v types.Volume) string {
	if v.OpenZFSConfiguration == nil {
		return ""
	}
	return aws.ToString(v.OpenZFSConfiguration.VolumePath)
}

func fsxQuotaGiB(bytes int64) int32 {
	return int32((bytes+(1<<30)-1)>>30) + 1
}

// createVolume is idempotent on token, so a task that died between the call and
// recording its answer gets the same volume back on its next attempt.
func (s *fsxStore) createVolume(ctx context.Context, token, name string, sizeBytes int64, origin string) (types.Volume, error) {
	cfg := &types.CreateOpenZFSVolumeConfiguration{
		ParentVolumeId:          aws.String(s.parent),
		StorageCapacityQuotaGiB: aws.Int32(fsxQuotaGiB(sizeBytes)),
		RecordSizeKiB:           aws.Int32(fsxRecordSizeKiB),
		DataCompressionType:     types.OpenZFSDataCompressionTypeLz4,
		NfsExports: []types.OpenZFSNfsExport{{
			ClientConfigurations: []types.OpenZFSClientConfiguration{{
				Clients: aws.String(s.clients),
				Options: fsxExportOptions,
			}},
		}},
	}
	if origin != "" {
		cfg.OriginSnapshot = &types.CreateOpenZFSOriginSnapshotConfiguration{
			SnapshotARN:  aws.String(origin),
			CopyStrategy: types.OpenZFSCopyStrategyClone,
		}
	}
	out, err := s.client.CreateVolume(ctx, &fsx.CreateVolumeInput{
		Name:                 aws.String(name),
		VolumeType:           types.VolumeTypeOpenzfs,
		ClientRequestToken:   aws.String(token),
		OpenZFSConfiguration: cfg,
	})
	if err != nil {
		return types.Volume{}, fmt.Errorf("could not create the volume %s: %w", name, err)
	}
	return *out.Volume, nil
}

func (s *fsxStore) waitVolume(ctx context.Context, id string) (types.Volume, error) {
	for {
		out, err := s.client.DescribeVolumes(ctx, &fsx.DescribeVolumesInput{VolumeIds: []string{id}})
		if err != nil {
			return types.Volume{}, fmt.Errorf("could not describe %s: %w", id, err)
		}
		if len(out.Volumes) != 1 {
			return types.Volume{}, fmt.Errorf("%s was not found", id)
		}
		v := out.Volumes[0]
		switch v.Lifecycle {
		case types.VolumeLifecycleAvailable:
			return v, nil
		case types.VolumeLifecycleFailed, types.VolumeLifecycleMisconfigured, types.VolumeLifecycleDeleting:
			reason := ""
			if v.LifecycleTransitionReason != nil {
				reason = ": " + aws.ToString(v.LifecycleTransitionReason.Message)
			}
			return types.Volume{}, fmt.Errorf("%s is %s%s", id, v.Lifecycle, reason)
		}
		if err := sleepCtx(ctx, fsxPoll); err != nil {
			return types.Volume{}, err
		}
	}
}

func (s *fsxStore) createSnapshot(ctx context.Context, token, name, volumeID string) (types.Snapshot, error) {
	out, err := s.client.CreateSnapshot(ctx, &fsx.CreateSnapshotInput{
		Name:               aws.String(name),
		VolumeId:           aws.String(volumeID),
		ClientRequestToken: aws.String(token),
	})
	if err != nil {
		return types.Snapshot{}, fmt.Errorf("could not snapshot %s: %w", volumeID, err)
	}
	return *out.Snapshot, nil
}

func (s *fsxStore) waitSnapshot(ctx context.Context, id string) error {
	for {
		out, err := s.client.DescribeSnapshots(ctx, &fsx.DescribeSnapshotsInput{SnapshotIds: []string{id}})
		if err != nil {
			return fmt.Errorf("could not describe %s: %w", id, err)
		}
		if len(out.Snapshots) != 1 {
			return fmt.Errorf("%s was not found", id)
		}
		switch sn := out.Snapshots[0]; sn.Lifecycle {
		case types.SnapshotLifecycleAvailable:
			return nil
		case types.SnapshotLifecycleDeleting:
			return fmt.Errorf("%s is being deleted", id)
		}
		if err := sleepCtx(ctx, fsxPoll); err != nil {
			return err
		}
	}
}

func (s *fsxStore) setQuota(ctx context.Context, volumeID string, sizeBytes int64) error {
	_, err := s.client.UpdateVolume(ctx, &fsx.UpdateVolumeInput{
		VolumeId: aws.String(volumeID),
		OpenZFSConfiguration: &types.UpdateOpenZFSVolumeConfiguration{
			StorageCapacityQuotaGiB: aws.Int32(fsxQuotaGiB(sizeBytes)),
		},
	})
	if err != nil {
		return fmt.Errorf("could not raise the quota of %s: %w", volumeID, err)
	}
	return nil
}

// deleteVolume takes the volume's own snapshots with it. A volume something
// is still cloned from is refused by FSx, which is the last guard for images.
func (s *fsxStore) deleteVolume(ctx context.Context, id string) error {
	_, err := s.client.DeleteVolume(ctx, &fsx.DeleteVolumeInput{
		VolumeId: aws.String(id),
		OpenZFSConfiguration: &types.DeleteVolumeOpenZFSConfiguration{
			Options: []types.DeleteOpenZFSVolumeOption{types.DeleteOpenZFSVolumeOptionDeleteChildVolumesAndSnapshots},
		},
	})
	var notFound *types.VolumeNotFound
	if err != nil && !errors.As(err, &notFound) {
		return fmt.Errorf("could not delete %s: %w", id, err)
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
