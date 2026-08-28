package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"control/internal/db"
)

const (
	settingClientOpenEnrollment = "client_open_enrollment"

	settingSignupsEnabled = "signups_enabled"

	settingDclientDownloadURL = "dclient_download_url"
	settingDpipeDownloadURL   = "dpipe_download_url"
	settingProxyDownloadURL   = "dproxy_download_url"

	settingVectorVersion      = "vector_version"
	settingClickHouseURL      = "clickhouse_url"
	settingClickHouseUser     = "clickhouse_user"
	settingClickHousePassword = "clickhouse_password"

	settingResolverUpstream = "resolver_upstream"

	settingReservedVMNames = "reserved_vm_names"
)

type settingKind string

const (
	settingBool   settingKind = "bool"
	settingString settingKind = "string"
	settingSecret settingKind = "secret"
)

type settingDef struct {
	Key         string      `json:"key"`
	Kind        settingKind `json:"kind"`
	Label       string      `json:"label"`
	Description string      `json:"description"`
	Placeholder string `json:"placeholder,omitempty"`
	Warn    bool   `json:"warn"`
	Default string `json:"-"`
	validate func(string) error `json:"-"`
}

var settingDefs = []settingDef{
	{
		Key:   settingSignupsEnabled,
		Kind:  settingBool,
		Label: "Email and password sign-ups",
		Description: "Let anyone create an account with an email and a password from the " +
			"sign-up page. With this off the form says so and the endpoint refuses. This " +
			"governs that form only -- each sign-in provider below decides separately " +
			"whether it may create accounts.",
		Default: "true",
	},
	{
		Key:   settingClientOpenEnrollment,
		Kind:  settingBool,
		Label: "Open client enrollment",
		Description: "Let a machine enroll itself with no enrollment key. " +
			"Anything that can reach the enrollment endpoint can then join the fleet, " +
			"so leave this off unless the control plane is on a network you own.",
		Warn:    true,
		Default: "false",
	},
	{
		Key:   settingDclientDownloadURL,
		Kind:  settingString,
		Label: "dclient download URL",
		Description: "Where hosts fetch dclient from, unless the host's own page names something " +
			"else. Empty means the published release for this control server's version, which is " +
			"what a normal installation wants -- set this to run builds that were never released, " +
			"such as from a local artifact server. Either the binary itself or a .tar.gz holding " +
			"it, decided by the suffix. Nothing is upgraded by saving this: it is the source a " +
			"host uses when it installs, and installing is per host.",
		Placeholder: "http://10.68.0.1:8081/backstage/dclient/dclient",
		validate:    validateServiceDownloadURL,
	},
	{
		Key:         settingDpipeDownloadURL,
		Kind:        settingString,
		Label:       "dpipe download URL",
		Description: "The same, for dpipe.",
		Placeholder: "http://10.68.0.1:8081/backstage/dpipe/dpipe",
		validate:    validateServiceDownloadURL,
	},
	{
		Key:         settingProxyDownloadURL,
		Kind:        settingString,
		Label:       "dproxy download URL",
		Description: "The same, for dproxy.",
		Placeholder: "http://10.68.0.1:8081/backstage/dproxy/dproxy",
		validate:    validateServiceDownloadURL,
	},
	{
		Key:   settingVectorVersion,
		Kind:  settingString,
		Label: "Vector version",
		Description: "The vector release each client downloads and runs to ship suricata's " +
			"events to clickhouse. Changing it re-installs vector on every host on its next " +
			"connect, so move it deliberately.",
		Placeholder: "0.57.0",
		Default:     "0.57.0",
		validate:    validateVectorVersion,
	},
	{
		Key:   settingClickHouseURL,
		Kind:  settingString,
		Label: "ClickHouse URL",
		Description: "The HTTP endpoint vector on each host ships events to. Not this server's " +
			"own clickhouse connection, which is CLICKHOUSE_URL in the environment and is usually " +
			"a different address and protocol -- this one has to be reachable from every qemu host, " +
			"not just from the control server. Empty stops vector from being installed at all.",
		Placeholder: "http://10.68.0.1:8123",
		validate:    validateClickHouseURL,
	},
	{
		Key:         settingClickHouseUser,
		Kind:        settingString,
		Label:       "ClickHouse user",
		Description: "The account clients authenticate to clickhouse as.",
		Placeholder: "dummie",
		Default:     "dummie",
	},
	{
		Key:   settingClickHousePassword,
		Kind:  settingSecret,
		Label: "ClickHouse password",
		Description: "Written to /etc/vector/vector.yaml on every host, so it is only as " +
			"private as the least private machine in the fleet. Never shown again once saved.",
	},
	{
		Key:   settingResolverUpstream,
		Kind:  settingString,
		Label: "Resolver upstream",
		Description: "Where the resolver on each qemu host forwards the lookups it is willing " +
			"to answer. Guests never reach it themselves -- they only ever talk to their own " +
			"gateway -- so this is the one place a fleet decides who sees its dns.",
		Placeholder: "1.1.1.1",
		Default:     "1.1.1.1",
		validate:    validateResolverUpstream,
	},
	{
		Key:   settingReservedVMNames,
		Kind:  settingString,
		Label: "Disallowed VM names",
		Description: "Further names nobody may give a VM, comma separated. A VM's name is the " +
			"host its http route is published under, so a name that collides with one of the " +
			"control plane's own hostnames would shadow it for the whole fleet -- which is why " +
			"www, console, shell and int are always refused and are not listed here. Existing " +
			"VMs keep the names they already have.",
		Placeholder: "status, api",
		validate:    validateReservedVMNames,
	},
}

func validateReservedVMNames(v string) error {
	for _, name := range parseReservedVMNames(v) {
		if !vmNamePattern.MatchString(name) {
			return fmt.Errorf("%q is not a name anyone could request: use lowercase letters, digits and single hyphens", name)
		}
	}
	return nil
}

func parseReservedVMNames(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if name := strings.ToLower(strings.TrimSpace(part)); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func validateResolverUpstream(v string) error {
	if v == "" {
		return errors.New("a resolver upstream is required; guests cannot resolve anything without one")
	}
	host := v
	if h, _, err := net.SplitHostPort(v); err == nil {
		host = h
	}
	if net.ParseIP(host) == nil {
		return errors.New("must be an IP address, optionally with a port like 10.0.0.1:5353")
	}
	return nil
}

func validateVectorVersion(v string) error {
	if !releaseVersionRe.MatchString(v) {
		return errors.New("must be a release number like 0.57.0")
	}
	return nil
}

func validateClickHouseURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return errors.New("must be a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("must be an http or https URL")
	}
	if u.Host == "" {
		return errors.New("must include a host, e.g. http://10.0.0.1:8123")
	}
	return nil
}

func settingDefByKey(key string) (settingDef, bool) {
	for _, d := range settingDefs {
		if d.Key == key {
			return d, true
		}
	}
	return settingDef{}, false
}

func parseSettingBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func formatSettingBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func setting(ctx context.Context, q *db.Queries, key string) string {
	def, ok := settingDefByKey(key)
	if !ok {
		return ""
	}
	row, err := q.GetSetting(ctx, key)
	if err != nil {
		return def.Default
	}
	return row.Value
}

func boolSetting(ctx context.Context, q *db.Queries, key string) bool {
	return parseSettingBool(setting(ctx, q, key))
}
