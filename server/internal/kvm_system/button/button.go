package button

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// Linux input_event struct layout on RISC-V 64-bit:
//
//	struct timeval { int64 tv_sec; int64 tv_usec; }  // 16 bytes
//	uint16 type
//	uint16 code
//	int32  value
//
// Total: 24 bytes, little-endian.
const (
	evKey          = 1  // EV_KEY
	inputEventSize = 24 // sizeof(struct input_event) on 64-bit Linux

	keyLongPress     = 1500 * time.Millisecond
	keyLongLongPress = 9000 * time.Millisecond
)

const inputDevice = "/dev/input/event0"

// Event types for button actions.
type Event int

const (
	EventShortPress    Event = iota // <1.5s
	EventLongPress                  // 1.5s–9s
	EventVeryLongPress              // >9s
)

func (e Event) String() string {
	switch e {
	case EventShortPress:
		return "short"
	case EventLongPress:
		return "long"
	case EventVeryLongPress:
		return "verylong"
	default:
		return fmt.Sprintf("unknown(%d)", int(e))
	}
}

// Handler reads the physical button from /dev/input/event0 and classifies
// presses by duration, matching the C++ thread_key_handle behaviour.
type Handler struct {
	eventCh chan Event
	device  string
}

func New() *Handler {
	return &Handler{
		eventCh: make(chan Event, 10),
		device:  inputDevice,
	}
}

// Events returns a read-only channel that receives classified button events.
func (h *Handler) Events() <-chan Event {
	return h.eventCh
}

// Run opens the input device and reads key events until ctx is cancelled.
// It classifies each press/release pair by duration and sends the result on
// the Events channel. Run blocks; call it from a goroutine.
func (h *Handler) Run(ctx context.Context) error {
	f, err := os.Open(h.device)
	if err != nil {
		return fmt.Errorf("open %s: %w", h.device, err)
	}
	defer f.Close()

	// Close file when the context is cancelled so the blocking read unblocks.
	go func() {
		<-ctx.Done()
		f.Close() // safe to double-close; unblocks the Read
	}()

	buf := make([]byte, inputEventSize)
	var pressTime time.Time

	for {
		// Check cancellation before each read.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		n, err := f.Read(buf)
		if err != nil {
			// Expected when context cancelled and file closed.
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			return fmt.Errorf("read %s: %w", h.device, err)
		}
		if n < inputEventSize {
			continue
		}

		evType := binary.LittleEndian.Uint16(buf[16:18])
		// buf[18:20] is code (unused — single button)
		evValue := int32(binary.LittleEndian.Uint32(buf[20:24]))

		if evType != evKey {
			continue
		}

		switch evValue {
		case 1: // key press
			pressTime = time.Now()

		case 0: // key release
			if pressTime.IsZero() {
				continue
			}
			duration := time.Since(pressTime)
			pressTime = time.Time{}

			var ev Event
			switch {
			case duration >= keyLongLongPress:
				ev = EventVeryLongPress
			case duration >= keyLongPress:
				ev = EventLongPress
			default:
				ev = EventShortPress
			}

			log.Debugf("button: %s press (%v)", ev, duration.Round(time.Millisecond))

			select {
			case h.eventCh <- ev:
			default:
				log.Warn("button: event channel full, dropping event")
			}
		}
	}
}
