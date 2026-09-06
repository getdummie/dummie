package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"control/internal/db"
	"control/internal/proto"
)

// The fleet's ssh identities live here rather than on the hosts. They are not
// in settingDefs: nothing about them is for an operator to type, and the
// private halves must never reach the settings API.
const (
	settingDpipeHostKey   = "dpipe_ssh_host_key"
	settingDpipeClientKey = "dpipe_ssh_client_key"
)

func fleetSSHKeys(ctx context.Context, q *db.Queries) (*proto.DpipeSSHKeys, error) {
	host, hostPub, err := fleetSSHKey(ctx, q, settingDpipeHostKey, "dpipe-host")
	if err != nil {
		return nil, err
	}
	client, clientPub, err := fleetSSHKey(ctx, q, settingDpipeClientKey, "dpipe-client")
	if err != nil {
		return nil, err
	}
	return &proto.DpipeSSHKeys{
		HostKey: host, HostPub: hostPub,
		ClientKey: client, ClientPub: clientPub,
	}, nil
}

// fleetSSHKey returns the stored key for this setting, generating one the
// first time. The insert loses to whoever got there first and returns their
// row, so two control servers starting at once still settle on one key.
func fleetSSHKey(ctx context.Context, q *db.Queries, key, comment string) (string, string, error) {
	row, err := q.GetSetting(ctx, key)
	if err == nil && strings.TrimSpace(row.Value) != "" {
		pub, err := sshPublicKeyLine(row.Value, comment)
		if err != nil {
			return "", "", fmt.Errorf("the stored %s is unusable: %w", key, err)
		}
		return row.Value, pub, nil
	}

	generated, err := generateSSHPrivateKey(comment)
	if err != nil {
		return "", "", err
	}
	stored, err := q.InsertSettingIfAbsent(ctx, db.InsertSettingIfAbsentParams{Key: key, Value: generated})
	if err != nil {
		return "", "", fmt.Errorf("could not store %s: %w", key, err)
	}
	pub, err := sshPublicKeyLine(stored.Value, comment)
	if err != nil {
		return "", "", fmt.Errorf("the stored %s is unusable: %w", key, err)
	}
	return stored.Value, pub, nil
}

func generateSSHPrivateKey(comment string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("could not generate an ed25519 key: %w", err)
	}
	block, err := marshalEd25519PrivateKey(priv, comment)
	if err != nil {
		return "", fmt.Errorf("could not encode the ed25519 key: %w", err)
	}
	return string(pem.EncodeToMemory(block)), nil
}

// Which of the two x/crypto takes has moved between releases, so try both.
func marshalEd25519PrivateKey(priv ed25519.PrivateKey, comment string) (*pem.Block, error) {
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err == nil {
		return block, nil
	}
	if block, ptrErr := ssh.MarshalPrivateKey(&priv, comment); ptrErr == nil {
		return block, nil
	}
	return nil, err
}

func sshPublicKeyLine(privatePEM, comment string) (string, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privatePEM))
	if err != nil {
		return "", err
	}
	line := strings.TrimRight(string(ssh.MarshalAuthorizedKey(signer.PublicKey())), "\r\n")
	return line + " " + comment + "\n", nil
}
