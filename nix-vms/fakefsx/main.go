// fakefsx answers the slice of the FSx API control uses, backed by real OpenZFS
// datasets shared over NFS. Dev only: it checks no signatures.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

const (
	targetPrefix = "AWSSimbaAPIService_v20180301."
	account      = "000000000000"
	region       = "local-1"

	propID    = "fakefsx:id"
	propToken = "fakefsx:token"
)

type server struct {
	mu   sync.Mutex
	pool string
	fsID string
	dns  string
}

type apiError struct {
	status int
	kind   string
	msg    string
}

func (e *apiError) Error() string { return e.kind + ": " + e.msg }

func badRequest(format string, args ...any) error {
	return &apiError{http.StatusBadRequest, "BadRequest", fmt.Sprintf(format, args...)}
}

func notFound(kind, format string, args ...any) error {
	return &apiError{http.StatusBadRequest, kind, fmt.Sprintf(format, args...)}
}

func main() {
	listen := flag.String("listen", "10.68.0.10:8080", "address to serve the API on")
	pool := flag.String("pool", "fsx", "zfs dataset that is the file system's root volume")
	fsID := flag.String("fs-id", "fs-0fa4ef5a00000001", "file system id to answer to")
	dns := flag.String("dns", "10.68.0.10", "address hosts mount volumes from")
	flag.Parse()

	s := &server{pool: *pool, fsID: *fsID, dns: *dns}
	if err := s.ensureRoot(); err != nil {
		log.Fatalf("could not prepare %s: %v", *pool, err)
	}
	log.Printf("fakefsx serving %s (pool %s) on %s", *fsID, *pool, *listen)
	log.Fatal(http.ListenAndServe(*listen, s))
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), targetPrefix)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, badRequest("could not read the body"))
		return
	}

	s.mu.Lock()
	out, err := s.dispatch(op, body)
	s.mu.Unlock()

	if err != nil {
		log.Printf("%s: %v", op, err)
		writeError(w, err)
		return
	}
	log.Printf("%s: ok", op)
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_ = json.NewEncoder(w).Encode(out)
}

func writeError(w http.ResponseWriter, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		ae = &apiError{http.StatusInternalServerError, "InternalServerError", err.Error()}
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("X-Amzn-ErrorType", ae.kind)
	w.WriteHeader(ae.status)
	_ = json.NewEncoder(w).Encode(map[string]string{"__type": ae.kind, "Message": ae.msg})
}

func (s *server) dispatch(op string, body []byte) (any, error) {
	decode := func(v any) error {
		if err := json.Unmarshal(body, v); err != nil {
			return badRequest("invalid json: %v", err)
		}
		return nil
	}
	switch op {
	case "DescribeFileSystems":
		var in struct{ FileSystemIds []string }
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.describeFileSystems(in.FileSystemIds)
	case "CreateVolume":
		var in createVolumeInput
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.createVolume(in)
	case "DescribeVolumes":
		var in struct{ VolumeIds []string }
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.describeVolumes(in.VolumeIds)
	case "UpdateVolume":
		var in struct {
			VolumeId             string
			OpenZFSConfiguration struct{ StorageCapacityQuotaGiB *int }
		}
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.updateVolume(in.VolumeId, in.OpenZFSConfiguration.StorageCapacityQuotaGiB)
	case "DeleteVolume":
		var in struct{ VolumeId string }
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.deleteVolume(in.VolumeId)
	case "CreateSnapshot":
		var in struct{ ClientRequestToken, Name, VolumeId string }
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.createSnapshot(in.ClientRequestToken, in.Name, in.VolumeId)
	case "DescribeSnapshots":
		var in struct{ SnapshotIds []string }
		if err := decode(&in); err != nil {
			return nil, err
		}
		return s.describeSnapshots(in.SnapshotIds)
	}
	return nil, badRequest("fakefsx does not implement %q", op)
}

type dataset struct {
	name, id, token, mountpoint, origin string
	quota, recordsize                   int64
	compression                         string
}

type snapshot struct {
	name, id, token string
}

func zfs(args ...string) (string, error) {
	out, err := exec.Command("zfs", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("zfs %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func newID(prefix string) string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])[:17]
}

func (s *server) ensureRoot() error {
	ds, err := s.datasets()
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.name == s.pool && d.id != "" {
			return nil
		}
	}
	_, err = zfs("set", propID+"="+newID("fsvol-"), s.pool)
	return err
}

func (s *server) datasets() ([]dataset, error) {
	out, err := zfs("list", "-H", "-p", "-t", "filesystem", "-r",
		"-o", "name,"+propID+","+propToken+",mountpoint,origin,quota,recordsize,compression", s.pool)
	if err != nil {
		return nil, err
	}
	var ds []dataset
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 8 {
			continue
		}
		quota, _ := strconv.ParseInt(f[5], 10, 64)
		rs, _ := strconv.ParseInt(f[6], 10, 64)
		ds = append(ds, dataset{
			name: f[0], id: unset(f[1]), token: unset(f[2]), mountpoint: f[3],
			origin: unset(f[4]), quota: quota, recordsize: rs, compression: f[7],
		})
	}
	return ds, nil
}

func (s *server) snapshots() ([]snapshot, error) {
	out, err := zfs("list", "-H", "-t", "snapshot", "-r", "-o", "name,"+propID+","+propToken, s.pool)
	if err != nil {
		return nil, err
	}
	var ss []snapshot
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || unset(f[1]) == "" {
			continue
		}
		ss = append(ss, snapshot{name: f[0], id: f[1], token: unset(f[2])})
	}
	return ss, nil
}

func unset(v string) string {
	if v == "-" {
		return ""
	}
	return v
}

func (s *server) volumeByID(id string) (dataset, error) {
	ds, err := s.datasets()
	if err != nil {
		return dataset{}, err
	}
	for _, d := range ds {
		if d.id != "" && d.id == id {
			return d, nil
		}
	}
	return dataset{}, notFound("VolumeNotFound", "no volume %s", id)
}

func (s *server) snapshotByID(id string) (snapshot, error) {
	ss, err := s.snapshots()
	if err != nil {
		return snapshot{}, err
	}
	for _, sn := range ss {
		if sn.id == id {
			return sn, nil
		}
	}
	return snapshot{}, notFound("SnapshotNotFound", "no snapshot %s", id)
}

func (s *server) describeFileSystems(ids []string) (any, error) {
	for _, id := range ids {
		if id != s.fsID {
			return nil, notFound("FileSystemNotFound", "no file system %s", id)
		}
	}
	root, err := s.rootVolume()
	if err != nil {
		return nil, err
	}
	return map[string]any{"FileSystems": []any{map[string]any{
		"FileSystemId":   s.fsID,
		"FileSystemType": "OPENZFS",
		"Lifecycle":      "AVAILABLE",
		"DNSName":        s.dns,
		"ResourceARN":    fmt.Sprintf("arn:aws:fsx:%s:%s:file-system/%s", region, account, s.fsID),
		"OpenZFSConfiguration": map[string]any{
			"RootVolumeId":   root.id,
			"DeploymentType": "SINGLE_AZ_HA_2",
		},
	}}}, nil
}

func (s *server) rootVolume() (dataset, error) {
	ds, err := s.datasets()
	if err != nil {
		return dataset{}, err
	}
	for _, d := range ds {
		if d.name == s.pool {
			return d, nil
		}
	}
	return dataset{}, fmt.Errorf("the pool %s is missing", s.pool)
}

type createVolumeInput struct {
	ClientRequestToken   string
	Name                 string
	VolumeType           string
	OpenZFSConfiguration struct {
		ParentVolumeId          string
		StorageCapacityQuotaGiB *int
		RecordSizeKiB           *int
		DataCompressionType     string
		NfsExports              []struct {
			ClientConfigurations []struct {
				Clients string
				Options []string
			}
		}
		OriginSnapshot *struct {
			SnapshotARN  string
			CopyStrategy string
		}
	}
}

func (s *server) createVolume(in createVolumeInput) (any, error) {
	cfg := in.OpenZFSConfiguration
	if in.VolumeType != "OPENZFS" {
		return nil, badRequest("VolumeType must be OPENZFS")
	}
	if !validName(in.Name) {
		return nil, badRequest("invalid volume name %q", in.Name)
	}
	ds, err := s.datasets()
	if err != nil {
		return nil, err
	}
	if in.ClientRequestToken != "" {
		for _, d := range ds {
			if d.token == in.ClientRequestToken {
				return map[string]any{"Volume": s.volumeJSON(d, ds)}, nil
			}
		}
	}
	parent, err := s.volumeByID(cfg.ParentVolumeId)
	if err != nil {
		return nil, err
	}
	name := parent.name + "/" + in.Name
	for _, d := range ds {
		if d.name == name {
			return nil, badRequest("a volume named %s already exists under %s", in.Name, parent.id)
		}
	}

	opts := []string{"-o", propID + "=" + newID("fsvol-")}
	if in.ClientRequestToken != "" {
		opts = append(opts, "-o", propToken+"="+in.ClientRequestToken)
	}
	if cfg.StorageCapacityQuotaGiB != nil {
		opts = append(opts, "-o", fmt.Sprintf("quota=%dG", *cfg.StorageCapacityQuotaGiB))
	}
	if cfg.RecordSizeKiB != nil {
		opts = append(opts, "-o", fmt.Sprintf("recordsize=%dK", *cfg.RecordSizeKiB))
	}
	switch cfg.DataCompressionType {
	case "LZ4":
		opts = append(opts, "-o", "compression=lz4")
	case "ZSTD":
		opts = append(opts, "-o", "compression=zstd")
	case "NONE":
		opts = append(opts, "-o", "compression=off")
	}
	share, err := sharenfs(cfg.NfsExports)
	if err != nil {
		return nil, err
	}
	opts = append(opts, "-o", "sharenfs="+share)

	if o := cfg.OriginSnapshot; o != nil {
		if o.CopyStrategy != "CLONE" {
			return nil, badRequest("fakefsx only does the CLONE copy strategy")
		}
		snapID := o.SnapshotARN[strings.LastIndex(o.SnapshotARN, "/")+1:]
		sn, err := s.snapshotByID(snapID)
		if err != nil {
			return nil, err
		}
		_, err = zfs(append(append([]string{"clone"}, opts...), sn.name, name)...)
		if err != nil {
			return nil, err
		}
	} else if _, err := zfs(append(append([]string{"create"}, opts...), name)...); err != nil {
		return nil, err
	}

	if ds, err = s.datasets(); err != nil {
		return nil, err
	}
	for _, d := range ds {
		if d.name == name {
			return map[string]any{"Volume": s.volumeJSON(d, ds)}, nil
		}
	}
	return nil, fmt.Errorf("created %s but cannot find it", name)
}

// sharenfs turns FSx export rules into the property ZFS hands to exportfs.
func sharenfs(exports []struct {
	ClientConfigurations []struct {
		Clients string
		Options []string
	}
}) (string, error) {
	if len(exports) == 0 || len(exports[0].ClientConfigurations) == 0 {
		return "off", nil
	}
	c := exports[0].ClientConfigurations[0]
	if c.Clients == "" || strings.ContainsAny(c.Clients, ",; ") {
		return "", badRequest("invalid clients %q", c.Clients)
	}
	access := "ro"
	var rest []string
	for _, o := range c.Options {
		switch o {
		case "rw":
			access = "rw"
		case "ro":
		default:
			rest = append(rest, o)
		}
	}
	// FSx accepts mounts from unprivileged ports (clients use noresvport).
	rest = append(rest, "insecure")
	return strings.Join(append([]string{access + "=@" + c.Clients}, rest...), ","), nil
}

func validName(n string) bool {
	if n == "" || len(n) > 203 {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_:.-", r)) {
			return false
		}
	}
	return true
}

func (s *server) volumeJSON(d dataset, all []dataset) map[string]any {
	parentID := ""
	if i := strings.LastIndex(d.name, "/"); i >= 0 {
		for _, p := range all {
			if p.name == d.name[:i] {
				parentID = p.id
			}
		}
	}
	cfg := map[string]any{
		"ParentVolumeId":      parentID,
		"VolumePath":          d.mountpoint,
		"RecordSizeKiB":       d.recordsize >> 10,
		"DataCompressionType": strings.ToUpper(d.compression),
	}
	if d.quota > 0 {
		cfg["StorageCapacityQuotaGiB"] = d.quota >> 30
	}
	if d.origin != "" {
		if sn, err := s.snapshotByName(d.origin); err == nil {
			cfg["OriginSnapshot"] = map[string]any{"SnapshotARN": s.snapshotARN(sn), "CopyStrategy": "CLONE"}
		}
	}
	return map[string]any{
		"VolumeId":             d.id,
		"Name":                 d.name[strings.LastIndex(d.name, "/")+1:],
		"VolumeType":           "OPENZFS",
		"FileSystemId":         s.fsID,
		"Lifecycle":            "AVAILABLE",
		"ResourceARN":          fmt.Sprintf("arn:aws:fsx:%s:%s:volume/%s/%s", region, account, s.fsID, d.id),
		"OpenZFSConfiguration": cfg,
	}
}

func (s *server) snapshotByName(name string) (snapshot, error) {
	ss, err := s.snapshots()
	if err != nil {
		return snapshot{}, err
	}
	for _, sn := range ss {
		if sn.name == name {
			return sn, nil
		}
	}
	return snapshot{}, notFound("SnapshotNotFound", "no snapshot %s", name)
}

func (s *server) snapshotARN(sn snapshot) string {
	volID := ""
	if d, err := s.datasetOfSnapshot(sn); err == nil {
		volID = d.id
	}
	return fmt.Sprintf("arn:aws:fsx:%s:%s:snapshot/%s/%s", region, account, volID, sn.id)
}

func (s *server) datasetOfSnapshot(sn snapshot) (dataset, error) {
	ds, err := s.datasets()
	if err != nil {
		return dataset{}, err
	}
	base := sn.name[:strings.Index(sn.name, "@")]
	for _, d := range ds {
		if d.name == base {
			return d, nil
		}
	}
	return dataset{}, fmt.Errorf("no dataset for %s", sn.name)
}

func (s *server) describeVolumes(ids []string) (any, error) {
	ds, err := s.datasets()
	if err != nil {
		return nil, err
	}
	var out []any
	for _, id := range ids {
		found := false
		for _, d := range ds {
			if d.id == id {
				out = append(out, s.volumeJSON(d, ds))
				found = true
			}
		}
		if !found {
			return nil, notFound("VolumeNotFound", "no volume %s", id)
		}
	}
	return map[string]any{"Volumes": out}, nil
}

func (s *server) updateVolume(id string, quotaGiB *int) (any, error) {
	d, err := s.volumeByID(id)
	if err != nil {
		return nil, err
	}
	if quotaGiB != nil {
		if _, err := zfs("set", fmt.Sprintf("quota=%dG", *quotaGiB), d.name); err != nil {
			return nil, err
		}
	}
	ds, err := s.datasets()
	if err != nil {
		return nil, err
	}
	d, _ = s.volumeByID(id)
	return map[string]any{"Volume": s.volumeJSON(d, ds)}, nil
}

// deleteVolume always takes the volume's snapshots with it, which is what control
// asks for. A snapshot something is cloned from makes zfs refuse, as FSx does.
func (s *server) deleteVolume(id string) (any, error) {
	d, err := s.volumeByID(id)
	if err != nil {
		return nil, err
	}
	if d.name == s.pool {
		return nil, badRequest("the root volume cannot be deleted")
	}
	if _, err := zfs("destroy", "-r", d.name); err != nil {
		return nil, badRequest("%v", err)
	}
	return map[string]any{"VolumeId": id, "Lifecycle": "DELETING"}, nil
}

func (s *server) createSnapshot(token, name, volumeID string) (any, error) {
	if !validName(name) {
		return nil, badRequest("invalid snapshot name %q", name)
	}
	if token != "" {
		ss, err := s.snapshots()
		if err != nil {
			return nil, err
		}
		for _, sn := range ss {
			if sn.token == token {
				return map[string]any{"Snapshot": s.snapshotJSON(sn)}, nil
			}
		}
	}
	d, err := s.volumeByID(volumeID)
	if err != nil {
		return nil, err
	}
	full := d.name + "@" + name
	args := []string{"snapshot", "-o", propID + "=" + newID("fsvolsnap-")}
	if token != "" {
		args = append(args, "-o", propToken+"="+token)
	}
	if _, err := zfs(append(args, full)...); err != nil {
		return nil, badRequest("%v", err)
	}
	sn, err := s.snapshotByName(full)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Snapshot": s.snapshotJSON(sn)}, nil
}

func (s *server) snapshotJSON(sn snapshot) map[string]any {
	volID := ""
	if d, err := s.datasetOfSnapshot(sn); err == nil {
		volID = d.id
	}
	return map[string]any{
		"SnapshotId":  sn.id,
		"Name":        sn.name[strings.Index(sn.name, "@")+1:],
		"VolumeId":    volID,
		"ResourceARN": s.snapshotARN(sn),
		"Lifecycle":   "AVAILABLE",
	}
}

func (s *server) describeSnapshots(ids []string) (any, error) {
	var out []any
	for _, id := range ids {
		sn, err := s.snapshotByID(id)
		if err != nil {
			return nil, err
		}
		out = append(out, s.snapshotJSON(sn))
	}
	return map[string]any{"Snapshots": out}, nil
}
