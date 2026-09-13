package firmware

// gadget.go bridges the firmware Controller to the usbgadget package, which is
// the sole owner of the USB gadget configfs (/sys/kernel/config/usb_gadget/g0).
// The Controller only tracks the higher-level "presented" state its capsule
// write cycle depends on; every raw configfs write — lun.0/lun.1 backing files,
// UDC bind/unbind, LUN creation — lives in usbgadget.

import (
	"context"
	"log/slog"

	"github.com/pi-bmc/nanokvm-app/pkg/platform/telemetry"
)

// presentVolume presents the FMP capsule volume on the gadget's lun.0.
// Idempotent. Must be called with c.mu held.
//
// The image file is presented directly (not a loop device) as a writable
// removable disk: the host's firmware deletes each capsule from
// \EFI\UpdateCapsule\ once it has applied it. The BMC only ever touches the
// volume while it is unpresented (see withVolume), so the two never write
// through the same bytes at once.
//
// ctx is threaded only as far as usbgadget.PresentDisk's own EBUSY retry loop
// — see its doc comment.
func (c *Controller) presentVolume(ctx context.Context) error {
	if c.presented {
		return nil
	}
	if err := c.massStorage().PresentDisk(ctx, c.capsulePath); err != nil {
		return err
	}
	c.presented = true
	// context.Background is deliberate, not an omission. This records the
	// gadget's resulting state, and it is reached from paths that have no
	// caller context at all (Init at boot, withVolume's unpresent/re-present
	// around a write). Binding it to a request context would also make the
	// record disappear exactly when a client disconnected mid-operation —
	// while the gadget stayed presented.
	telemetry.FirmwarePresented(context.Background(), true)
	c.log.Info("firmware: presented capsule volume via USB gadget", slog.String("path", c.capsulePath))
	return nil
}

// unpresentVolume clears the capsule volume from lun.0. After this returns the
// backing file is no longer held by f_mass_storage and is safe to rewrite.
// Must be called with c.mu held.
func (c *Controller) unpresentVolume() error {
	if !c.presented {
		return nil
	}
	if err := c.massStorage().UnpresentDisk(); err != nil {
		return err
	}
	c.presented = false
	// See presentVolume for why this is not a caller's context.
	telemetry.FirmwarePresented(context.Background(), false)
	c.log.Info("firmware: unpresented USB gadget")
	return nil
}

// presentIfPendingLocked attaches the capsule volume to lun.0 when at least one
// capsule is waiting for the host and detaches it when none are. Both halves
// are idempotent, so this is safe to call on every path that can change or
// observe the capsule set.
//
// The volume is attached only while there is something to apply. An empty
// \EFI\UpdateCapsule\ gives host firmware nothing to find, so a permanently
// attached LUN just adds a removable device for the host to enumerate and scan
// at every boot. Detaching also means the host's own view answers "is an update
// pending?" without asking the BMC.
//
// Callers that have just listed the volume pass their own count rather than
// paying for a second read; see reconcilePresentationLocked for the rest.
// Must hold c.mu.
func (c *Controller) presentIfPendingLocked(ctx context.Context, pending int) error {
	if pending > 0 {
		return c.presentVolume(ctx)
	}
	return c.unpresentVolume()
}

// reconcilePresentationLocked reads the capsule volume and applies
// presentIfPendingLocked to what it finds. Must hold c.mu.
//
// This must never be called from listCapsulesLocked or from the read path of
// withVolume: it calls the former, and doing so would recurse.
func (c *Controller) reconcilePresentationLocked(ctx context.Context) error {
	capsules, err := c.listCapsulesLocked()
	if err != nil {
		return err
	}
	return c.presentIfPendingLocked(ctx, len(capsules))
}

// Present presents the capsule volume via USB gadget (public, acquires lock).
func (c *Controller) Present(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.presentVolume(ctx)
}

// Unpresent removes the capsule volume from the USB gadget (public, acquires
// lock). The host sees an empty drive until Present is called again.
func (c *Controller) Unpresent() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unpresentVolume()
}
