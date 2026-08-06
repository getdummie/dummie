package dpipe

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// exitStatusGrace bounds how long a relayed channel waits for trailing channel
// requests (typically exit-status) after both data directions have hit EOF.
const exitStatusGrace = 30 * time.Second

// relaySSH wires two established SSH connections together: global requests both
// ways, and channels opened from either end (sessions, direct-tcpip for -L,
// forwarded-tcpip for -R).
func relaySSH(log *slog.Logger,
	client ssh.Conn, clientChans <-chan ssh.NewChannel, clientReqs <-chan *ssh.Request,
	vm ssh.Conn, vmChans <-chan ssh.NewChannel, vmReqs <-chan *ssh.Request,
) {
	go relayGlobalRequests(clientReqs, vm)
	go relayGlobalRequests(vmReqs, client)
	go relayNewChannels(log, clientChans, vm)
	go relayNewChannels(log, vmChans, client)

	done := make(chan struct{}, 2)
	go func() { _ = client.Wait(); done <- struct{}{} }()
	go func() { _ = vm.Wait(); done <- struct{}{} }()
	<-done

	_ = client.Close()
	_ = vm.Close()
}

func relayGlobalRequests(in <-chan *ssh.Request, out ssh.Conn) {
	for req := range in {
		ok, payload, err := out.SendRequest(req.Type, req.WantReply, req.Payload)
		if req.WantReply {
			_ = req.Reply(ok && err == nil, payload)
		}
	}
}

func relayNewChannels(log *slog.Logger, in <-chan ssh.NewChannel, out ssh.Conn) {
	for nch := range in {
		go openAndRelay(log, nch, out)
	}
}

func openAndRelay(log *slog.Logger, nch ssh.NewChannel, out ssh.Conn) {
	far, farReqs, err := out.OpenChannel(nch.ChannelType(), nch.ExtraData())
	if err != nil {
		var oce *ssh.OpenChannelError
		if errors.As(err, &oce) {
			_ = nch.Reject(oce.Reason, oce.Message)
		} else {
			_ = nch.Reject(ssh.ConnectionFailed, err.Error())
		}
		log.Debug("ssh channel rejected", "type", nch.ChannelType(), "err", err)
		return
	}
	near, nearReqs, err := nch.Accept()
	if err != nil {
		_ = far.Close()
		log.Debug("ssh channel accept failed", "type", nch.ChannelType(), "err", err)
		return
	}
	log.Debug("ssh channel open", "type", nch.ChannelType())
	relayChannel(near, nearReqs, far, farReqs)
	log.Debug("ssh channel closed", "type", nch.ChannelType())
}

// relayChannel copies data, extended data (stderr) and channel requests between
// two channels, honouring half-close and letting trailing requests such as
// exit-status through before closing.
func relayChannel(a ssh.Channel, aReqs <-chan *ssh.Request, b ssh.Channel, bReqs <-chan *ssh.Request) {
	var data sync.WaitGroup
	data.Add(2)
	go func() {
		defer data.Done()
		_, _ = io.Copy(b, a)
		_ = b.CloseWrite()
	}()
	go func() {
		defer data.Done()
		_, _ = io.Copy(a, b)
		_ = a.CloseWrite()
	}()
	go func() { _, _ = io.Copy(a.Stderr(), b.Stderr()) }()
	go func() { _, _ = io.Copy(b.Stderr(), a.Stderr()) }()

	reqs := make(chan struct{}, 2)
	go func() { relayChannelRequests(aReqs, b); reqs <- struct{}{} }()
	go func() { relayChannelRequests(bReqs, a); reqs <- struct{}{} }()

	data.Wait()

	// Give the peers a bounded window to deliver exit-status and close.
	timer := time.NewTimer(exitStatusGrace)
	defer timer.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-reqs:
		case <-timer.C:
			i = 2
		}
	}

	_ = a.Close()
	_ = b.Close()
}

func relayChannelRequests(in <-chan *ssh.Request, out ssh.Channel) {
	for req := range in {
		ok, err := out.SendRequest(req.Type, req.WantReply, req.Payload)
		if req.WantReply {
			_ = req.Reply(ok && err == nil, nil)
		}
	}
}
