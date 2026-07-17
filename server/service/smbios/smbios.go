// Package smbios reads the SMBIOS tables that U-Boot mirrors into the I2C
// EEPROM, and renders them as host inventory.
//
// U-Boot (CONFIG_SMBIOS_I2C_STORE) writes the tables it generates at boot into
// a region of the same 24c256 that carries the UEFI variable store and the
// environment:
//
//	0x0000..0x3fff  UEFI variable blob (efivars)
//	0x4000..0x5fff  U-Boot environment (ubootenv)
//	0x6000..0x67ff  SMBIOS tables      (this package)
//
// The blob is exactly what U-Boot generated: a 24-byte _SM3_ entry point
// followed - 16-byte aligned - by the structure table. These are the same
// bytes the host OS sees, and they carry strictly more than the environment
// does: the type 1 UUID, the real product name and version, SKU, and the
// processor/cache detail exist nowhere else. So the BMC prefers this over the
// environment for inventory.
//
// Unlike efivars/ubootenv this store is read-only here - only the host writes
// it - so there is no snapshot to reconcile. A blank region simply reports
// ErrNoTables and callers fall back to the environment.
package smbios

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	gosmbios "github.com/siderolabs/go-smbios/smbios"
)

// Backend reads the raw bytes backing the store using absolute offsets into
// the device. The efivars file and I2C backends satisfy it structurally, so
// every store shares one EEPROM device without this package depending on them.
type Backend interface {
	ReadAt(off int, p []byte) error
	Size() int
}

const (
	// cacheTTL bounds how long parsed tables are served without re-reading
	// the EEPROM. The host only rewrites them across a U-Boot update or a
	// serial#/DT change, so this can be generous.
	cacheTTL = 30 * time.Second

	// entryPointLen is sizeof(struct smbios3_entry) in U-Boot.
	entryPointLen = 24

	// tableAlign mirrors write_smbios_table(), which starts the structure
	// table at ALIGN(addr + sizeof(struct smbios3_entry), 16).
	tableAlign = 16
)

var (
	// ErrNotConfigured is returned by a Store with no backend wired up.
	ErrNotConfigured = errors.New("smbios: store not configured")
	// ErrNoTables is returned when the region holds no valid entry point -
	// a blank EEPROM, or a host that has not booted this firmware yet.
	ErrNoTables = errors.New("smbios: no tables in the store")

	anchor = [5]byte{'_', 'S', 'M', '3', '_'}
)

// entryPoint mirrors U-Boot's packed struct smbios3_entry (little-endian).
type entryPoint struct {
	Anchor       [5]byte
	Checksum     uint8
	Length       uint8
	MajorVer     uint8
	MinorVer     uint8
	DocRev       uint8
	Revision     uint8
	Reserved     uint8
	TableMaxSize uint32
	TableAddress uint64
}

// Info is the host inventory derived from the tables. Fields are empty when
// the corresponding structure or string is absent.
type Info struct {
	// Type 1 - System Information.
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
	Version      string `json:"version,omitempty"`
	Serial       string `json:"serial,omitempty"`
	UUID         string `json:"uuid,omitempty"`
	SKU          string `json:"sku,omitempty"`
	Family       string `json:"family,omitempty"`

	// Type 0 - BIOS Information.
	BIOSVendor  string `json:"biosVendor,omitempty"`
	BIOSVersion string `json:"biosVersion,omitempty"`
	BIOSDate    string `json:"biosDate,omitempty"`

	// Type 2 - Baseboard Information.
	BoardManufacturer string `json:"boardManufacturer,omitempty"`
	BoardProduct      string `json:"boardProduct,omitempty"`
	BoardSerial       string `json:"boardSerial,omitempty"`

	// Type 4 - Processor Information (first socket).
	CPUManufacturer string `json:"cpuManufacturer,omitempty"`
	CPUVersion      string `json:"cpuVersion,omitempty"`
	CPUPartNumber   string `json:"cpuPartNumber,omitempty"`
	CPUCores        int    `json:"cpuCores,omitempty"`
	CPUThreads      int    `json:"cpuThreads,omitempty"`
	CPUMaxSpeedMHz  int    `json:"cpuMaxSpeedMHz,omitempty"`

	// SMBIOSVersion is the spec version the host reported, e.g. "3.7.0".
	SMBIOSVersion string `json:"smbiosVersion,omitempty"`
}

// Store reads the SMBIOS tables from a fixed [offset, offset+size) region of
// an EEPROM. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	backend Backend
	offset  int
	size    int

	cache     *Info
	cacheTime time.Time
}

// NewStore returns a Store over the given EEPROM region.
func NewStore(b Backend, offset, size int) *Store {
	return &Store{backend: b, offset: offset, size: size}
}

// Available reports whether a backend is configured.
func (s *Store) Available() bool { return s != nil && s.backend != nil }

// Invalidate drops the cached tables, forcing the next read to hit the EEPROM.
func (s *Store) Invalidate() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheTime = time.Time{}
}

// Load returns the parsed inventory. It reports ErrNoTables when the region is
// blank or holds no valid entry point, so callers can fall back.
func (s *Store) Load() (*Info, error) {
	if !s.Available() {
		return nil, ErrNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache != nil && !s.cacheTime.IsZero() && time.Since(s.cacheTime) < cacheTTL {
		return s.cache, nil
	}

	raw := make([]byte, s.size)
	if err := s.backend.ReadAt(s.offset, raw); err != nil {
		return nil, fmt.Errorf("smbios: read region at %#x: %w", s.offset, err)
	}

	info, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	s.cache, s.cacheTime = info, time.Now()
	return info, nil
}

// Parse decodes a raw region: the _SM3_ entry point followed by the structure
// table. Exported so the region can be parsed from a file or a test fixture.
func Parse(raw []byte) (*Info, error) {
	if len(raw) < entryPointLen {
		return nil, ErrNoTables
	}

	var ep entryPoint
	if err := binary.Read(bytes.NewReader(raw[:entryPointLen]), binary.LittleEndian, &ep); err != nil {
		return nil, fmt.Errorf("smbios: decode entry point: %w", err)
	}
	if ep.Anchor != anchor {
		return nil, ErrNoTables
	}
	if ep.Length < entryPointLen || int(ep.Length) > len(raw) {
		return nil, fmt.Errorf("smbios: bogus entry point length %d", ep.Length)
	}
	if !checksumOK(raw[:ep.Length]) {
		return nil, errors.New("smbios: entry point checksum mismatch")
	}

	// The tables follow the entry point, 16-byte aligned, exactly as
	// write_smbios_table() lays them out. The entry point's TableAddress is
	// the DRAM address they were generated at and is meaningless here.
	start := align(int(ep.Length), tableAlign)
	end := start + int(ep.TableMaxSize)
	if ep.TableMaxSize == 0 || end > len(raw) {
		return nil, fmt.Errorf("smbios: table of %d bytes does not fit the %d-byte region",
			ep.TableMaxSize, len(raw))
	}

	version := gosmbios.Version{
		Major:    int(ep.MajorVer),
		Minor:    int(ep.MinorVer),
		Revision: int(ep.DocRev),
	}
	s, err := gosmbios.Decode(bytes.NewReader(raw[start:end]), version)
	if err != nil {
		return nil, fmt.Errorf("smbios: decode tables: %w", err)
	}

	return infoFrom(s, version), nil
}

func infoFrom(s *gosmbios.SMBIOS, v gosmbios.Version) *Info {
	info := &Info{
		SMBIOSVersion: fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Revision),

		Manufacturer: s.SystemInformation.Manufacturer,
		Product:      s.SystemInformation.ProductName,
		Version:      s.SystemInformation.Version,
		Serial:       s.SystemInformation.SerialNumber,
		UUID:         s.SystemInformation.UUID,
		SKU:          s.SystemInformation.SKUNumber,
		Family:       s.SystemInformation.Family,

		BIOSVendor:  s.BIOSInformation.Vendor,
		BIOSVersion: s.BIOSInformation.Version,
		BIOSDate:    s.BIOSInformation.ReleaseDate,

		BoardManufacturer: s.BaseboardInformation.Manufacturer,
		BoardProduct:      s.BaseboardInformation.Product,
		BoardSerial:       s.BaseboardInformation.SerialNumber,
	}

	if len(s.ProcessorInformation) > 0 {
		p := s.ProcessorInformation[0]
		info.CPUManufacturer = p.ProcessorManufacturer
		info.CPUVersion = p.ProcessorVersion
		info.CPUPartNumber = p.PartNumber
		info.CPUCores = int(p.CoreCount)
		info.CPUThreads = int(p.ThreadCount)
		info.CPUMaxSpeedMHz = int(p.MaxSpeed)
	}

	return info
}

// checksumOK verifies the entry point's 8-bit sum-to-zero checksum.
func checksumOK(b []byte) bool {
	var sum uint8
	for _, c := range b {
		sum += c
	}
	return sum == 0
}

func align(n, a int) int { return (n + a - 1) &^ (a - 1) }
