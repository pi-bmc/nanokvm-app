package usbgadget

import (
	"fmt"

	log "github.com/sirupsen/logrus"
)

// SetEthernet selects the USB ethernet function mode ("off"|"ecm"|"ncm"),
// persists it, and reconciles the gadget. A mode change triggers a UDC
// unbind/rebind so the host re-enumerates.
func (g *Gadget) SetEthernet(mode string) error {
	switch mode {
	case EthernetOff, EthernetECM, EthernetNCM:
	default:
		return fmt.Errorf("invalid ethernet mode %q", mode)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if mode == g.state.Ethernet {
		return nil
	}
	if mode != EthernetOff {
		if err := g.ensureEthernetFunc(mode); err != nil {
			return err
		}
	}
	g.state.Ethernet = mode
	if err := saveState(g.cfg.StatePath, g.state); err != nil {
		log.Warnf("usbgadget: persist state: %v", err)
	}
	return g.reconcileLinks()
}

// SetDisk toggles whether mass_storage.disk0 is exposed to the host (linked into
// configs/c.1). The function directory and its LUNs are never removed.
func (g *Gadget) SetDisk(on bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if on == g.state.Disk {
		return nil
	}
	g.state.Disk = on
	if err := saveState(g.cfg.StatePath, g.state); err != nil {
		log.Warnf("usbgadget: persist state: %v", err)
	}
	return g.reconcileLinks()
}
