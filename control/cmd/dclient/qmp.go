package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const qmpTimeout = 5 * time.Second

func qmpCommand(ctx context.Context, socket, command string) error {
	d := net.Dialer{}
	cctx, cancel := context.WithTimeout(ctx, qmpTimeout)
	defer cancel()

	conn, err := d.DialContext(cctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("could not reach qmp at %s: %w", socket, err)
	}
	defer conn.Close()

	if deadline, ok := cctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	r := bufio.NewReader(conn)
	if _, err := readQMP(r); err != nil {
		return err
	}
	if err := execQMP(conn, r, "qmp_capabilities"); err != nil {
		return err
	}
	return execQMP(conn, r, command)
}

func execQMP(conn net.Conn, r *bufio.Reader, command string) error {
	req, err := json.Marshal(map[string]string{"execute": command})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return err
	}

	for {
		msg, err := readQMP(r)
		if err != nil {
			return err
		}
		if msg.Error != nil {
			return fmt.Errorf("qmp %s failed: %s", command, msg.Error.Desc)
		}
		if msg.Return != nil {
			return nil
		}
	}
}

type qmpMessage struct {
	Return json.RawMessage `json:"return"`
	Error  *struct {
		Class string `json:"class"`
		Desc  string `json:"desc"`
	} `json:"error"`
}

func readQMP(r *bufio.Reader) (qmpMessage, error) {
	var msg qmpMessage
	line, err := r.ReadBytes('\n')
	if err != nil {
		return msg, fmt.Errorf("qmp read failed: %w", err)
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return msg, fmt.Errorf("qmp sent something unparseable: %w", err)
	}
	return msg, nil
}
