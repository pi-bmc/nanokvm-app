package ipmi

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/creack/pty"
	log "github.com/sirupsen/logrus"
)

// solState manages a single SOL serial connection.
type solState struct {
	mu     sync.Mutex
	active bool
	cmd    *exec.Cmd
	ptmx   *os.File
	outSeq byte
	sess   *session
	stopCh chan struct{}
}

var solSession = &solState{}

// handleActivatePayload starts a SOL session over the serial port.
func handleActivatePayload(sess *session, cmdData []byte) []byte {
	if len(cmdData) < 2 {
		return []byte{ccInvalidParam}
	}

	payloadType := cmdData[0] & 0x3F
	if payloadType != 0x01 {
		return []byte{ccInvalidParam}
	}

	solSession.mu.Lock()
	defer solSession.mu.Unlock()

	if solSession.active {
		return []byte{ccPayloadAlready}
	}

	cmd := exec.Command("picocom", "-b", defaultBaudRate, defaultSerialPort)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Errorf("SOL: failed to start picocom: %s", err)
		return []byte{ccUnspecified}
	}

	solSession.cmd = cmd
	solSession.ptmx = ptmx
	solSession.active = true
	solSession.outSeq = 0
	solSession.sess = sess
	solSession.stopCh = make(chan struct{})

	go solSession.readLoop()

	log.Info("SOL: session activated")

	// Response: cc(1) + aux(4) + inbound_size(2) + outbound_size(2) + port(2) + vlan(2)
	resp := make([]byte, 13)
	resp[0] = ccOK
	// resp[1:5] = auxiliary data (zeros)
	resp[5] = 0x00
	resp[6] = 0x04 // inbound payload size = 1024
	resp[7] = 0x00
	resp[8] = 0x04 // outbound payload size = 1024
	resp[9] = 0x6F
	resp[10] = 0x02 // UDP port = 623
	// resp[11:13] = VLAN (zeros)
	return resp
}

// handleDeactivatePayload stops the active SOL session.
func handleDeactivatePayload(cmdData []byte) []byte {
	solSession.stop()
	return []byte{ccOK}
}

func (sol *solState) stop() {
	sol.mu.Lock()
	defer sol.mu.Unlock()

	if !sol.active {
		return
	}

	sol.active = false
	close(sol.stopCh)

	if sol.ptmx != nil {
		_ = sol.ptmx.Close()
	}
	if sol.cmd != nil && sol.cmd.Process != nil {
		_ = sol.cmd.Process.Kill()
		_ = sol.cmd.Wait()
	}
	sol.ptmx = nil
	sol.cmd = nil
	sol.sess = nil

	log.Info("SOL: session deactivated")
}

// readLoop reads from the serial PTY and sends SOL data to the remote console.
func (sol *solState) readLoop() {
	buf := make([]byte, 1024)

	for {
		select {
		case <-sol.stopCh:
			return
		default:
		}

		n, err := sol.ptmx.Read(buf)
		if err != nil {
			select {
			case <-sol.stopCh:
			default:
				log.Debugf("SOL: read error: %s", err)
			}
			return
		}

		if n > 0 {
			sol.sendData(buf[:n])
		}
	}
}

// sendData sends SOL payload data to the remote console.
func (sol *solState) sendData(data []byte) {
	sol.mu.Lock()
	if !sol.active || sol.sess == nil {
		sol.mu.Unlock()
		return
	}

	sol.outSeq++
	seq := sol.outSeq
	sess := sol.sess
	srv := sess.server
	addr := sess.addr
	sol.mu.Unlock()

	if srv == nil || addr == nil {
		return
	}

	// SOL payload: seq(1) + ackSeq(1) + acceptedChars(1) + status(1) + data
	solPayload := make([]byte, 4+len(data))
	solPayload[0] = seq
	// solPayload[1] = 0 — ack seq
	// solPayload[2] = 0 — accepted chars
	// solPayload[3] = 0 — status OK
	copy(solPayload[4:], data)

	outSeq := atomic.AddUint32(&sess.outSeq, 1)

	var pkt []byte
	if sess.integAlgo != integAlgoNone && sess.k1 != nil {
		pkt = wrapRMCP(rmcpClassIPMI,
			wrapRMCPPlusAuth(payloadTypeSOL, sess.consoleSessionID, outSeq, solPayload, sess.k1))
	} else {
		pkt = wrapRMCP(rmcpClassIPMI,
			wrapRMCPPlus(payloadTypeSOL, sess.consoleSessionID, outSeq, solPayload))
	}

	if _, err := srv.conn.WriteToUDP(pkt, addr); err != nil {
		log.Errorf("SOL: send error: %s", err)
	}
}

// SerialPort returns the configured serial port path for diagnostics.
func SerialPort() string {
	return fmt.Sprintf("%s @ %s baud", defaultSerialPort, defaultBaudRate)
}
