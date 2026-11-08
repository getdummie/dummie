package main

import (
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"control/internal/db"
)

var activityAdded = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func activityTarget(kind, dest, transport, ports string) db.VmNetworkTarget {
	return db.VmNetworkTarget{
		ID:   pgtype.UUID{Valid: true},
		Kind: kind, Destination: dest, Transport: transport, Ports: ports,
		CreatedAt: pgtype.Timestamptz{Time: activityAdded, Valid: true},
	}
}

func hostSeen(host, appProto string, at time.Time) egressSeen {
	return egressSeen{host: host, appProto: appProto, at: at}
}

func addrSeen(ip string, port uint16, at time.Time) egressSeen {
	return egressSeen{host: "example.com", appProto: "tls", destIP: net.ParseIP(ip), destPort: port, proto: "tcp", at: at}
}

func TestSeenMatches(t *testing.T) {
	after := activityAdded.Add(time.Hour)
	for _, tc := range []struct {
		name   string
		target db.VmNetworkTarget
		seen   egressSeen
		want   bool
	}{
		{"subdomain over tls", activityTarget("domain", "ubuntu.com", "", ""), hostSeen("archive.ubuntu.com", "tls", after), true},
		{"the domain itself", activityTarget("domain", "ubuntu.com", "", ""), hostSeen("ubuntu.com", "http", after), true},
		{"a lookalike suffix", activityTarget("domain", "ubuntu.com", "", ""), hostSeen("notubuntu.com", "tls", after), false},
		{"http on a tls-only domain", activityTarget("domain", "ubuntu.com", "", "443"), hostSeen("ubuntu.com", "http", after), false},
		{"a resolve-only domain", activityTarget("domain", "ubuntu.com", "", "none"), hostSeen("ubuntu.com", "tls", after), false},
		{"an address", activityTarget("ip", "1.2.3.4", "tcp", ""), addrSeen("::ffff:1.2.3.4", 443, after), true},
		{"another address", activityTarget("ip", "1.2.3.4", "tcp", ""), addrSeen("1.2.3.5", 443, after), false},
		{"a cidr", activityTarget("ip", "1.2.3.0/24", "any", ""), addrSeen("1.2.3.9", 80, after), true},
		{"the wrong transport", activityTarget("ip", "1.2.3.4", "udp", ""), addrSeen("1.2.3.4", 443, after), false},
		{"a port in range", activityTarget("ip", "1.2.3.4", "tcp", "22,400:500"), addrSeen("1.2.3.4", 443, after), true},
		{"a port out of range", activityTarget("ip", "1.2.3.4", "tcp", "22,1000:2000"), addrSeen("1.2.3.4", 443, after), false},
		{"everything", activityTarget("ip", targetEverywhere, "any", ""), addrSeen("8.8.8.8", 443, after), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := seenMatches(tc.target, tc.seen); got != tc.want {
				t.Errorf("seenMatches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTargetActivityIgnoresTrafficBeforeTheAllowance(t *testing.T) {
	before := activityAdded.Add(-time.Hour)
	after := activityAdded.Add(time.Hour)
	items := targetActivity(
		[]db.VmNetworkTarget{activityTarget("domain", "ubuntu.com", "", "")},
		[]egressSeen{
			hostSeen("security.ubuntu.com", "tls", before),
			hostSeen("archive.ubuntu.com", "http", after),
			hostSeen("ubuntu.com", "tls", after.Add(time.Minute)),
		},
	)
	got := items[0]
	if ip := targetActivity([]db.VmNetworkTarget{activityTarget("ip", "1.2.3.4", "tcp", "")}, []egressSeen{addrSeen("1.2.3.4", 443, after)})[0]; len(ip.Hosts) != 0 {
		t.Errorf("an address allowance listed hosts: %+v", ip.Hosts)
	}
	if got.LastConnected != after.Add(time.Minute).Format(time.RFC3339) {
		t.Errorf("last_connected = %q", got.LastConnected)
	}
	if len(got.Hosts) != 2 || got.Hosts[0].Host != "ubuntu.com" || got.Hosts[1].Host != "archive.ubuntu.com" {
		t.Errorf("hosts = %+v, want ubuntu.com then archive.ubuntu.com", got.Hosts)
	}
}
