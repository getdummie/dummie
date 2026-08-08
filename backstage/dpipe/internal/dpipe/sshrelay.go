package dpipe

import (
	"errors"
	"io"
	"log/slog"
	"time"

	"golang.org/x/crypto/ssh"
)

// exitStatusGrace bounds how long a relayed channel waits for trailing channel
// requests (typically exit-status) after the far side has hit EOF.
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
// The far side (b) closing is what ends the channel. Waiting on both directions
// would deadlock an interactive session: the client keeps its input side open
// until it sees our close, and we would not close until it did.
func relayChannel(a ssh.Channel, aReqs <-chan *ssh.Request, b ssh.Channel, bReqs <-chan *ssh.Request) {
	go func() {
		_, _ = io.Copy(b, a)
		_ = b.CloseWrite()
	}()
	farDone := make(chan struct{})
	go func() {
		defer close(farDone)
		_, _ = io.Copy(a, b)
		_ = a.CloseWrite()
	}()
	go func() { _, _ = io.Copy(a.Stderr(), b.Stderr()) }()
	go func() { _, _ = io.Copy(b.Stderr(), a.Stderr()) }()

	go relayChannelRequests(aReqs, b)
	farReqs := make(chan struct{})
	go func() { relayChannelRequests(bReqs, a); close(farReqs) }()

	<-farDone

	// exit-status arrives from b as a trailing request; let it through first.
	timer := time.NewTimer(exitStatusGrace)
	defer timer.Stop()
	select {
	case <-farReqs:
	case <-timer.C:
	}

	// Closing a unblocks the still-pending copy out of it.
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
