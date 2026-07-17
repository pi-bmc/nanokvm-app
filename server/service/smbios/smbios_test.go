package smbios

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
)

const (
	testRegionSize = 0x800
	testOffset     = 0x6000
	testChipSize   = 0x8000
)

// type1 builds an SMBIOS System Information structure: a 4-byte header, a
// 23-byte formatted area (length 0x1b total), then the string table.
func type1(uuid [16]byte, strs ...string) []byte {
	var b bytes.Buffer
	b.WriteByte(1)    // type
	b.WriteByte(0x1b) // length: header + formatted area
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	b.WriteByte(1) // manufacturer -> string 1
	b.WriteByte(2) // product name -> string 2
	b.WriteByte(3) // version      -> string 3
	b.WriteByte(4) // serial       -> string 4
	b.Write(uuid[:])
	b.WriteByte(6) // wake-up type: power switch
	b.WriteByte(5) // SKU          -> string 5
	b.WriteByte(6) // family       -> string 6
	for _, s := range strs {
		b.WriteString(s)
		b.WriteByte(0)
	}
	b.WriteByte(0) // end of the string table
	return b.Bytes()
}

// endOfTable builds the type 127 terminator.
func endOfTable() []byte {
	return []byte{127, 4, 0x7f, 0x00, 0x00, 0x00}
}

// buildRegion assembles the bytes exactly as U-Boot lays them out: the _SM3_
// entry point, then the structure table at ALIGN(entry.length, 16).
func buildRegion(t *testing.T, tables []byte) []byte {
	t.Helper()
	region := make([]byte, testRegionSize)
	copy(region[align(entryPointLen, tableAlign):], tables)

	ep := entryPoint{
		Anchor:       anchor,
		Length:       entryPointLen,
		MajorVer:     3,
		MinorVer:     7,
		DocRev:       0,
		Revision:     1,
		TableMaxSize: uint32(len(tables)),
		// A DRAM address, as U-Boot leaves it. Parsing must ignore this.
		TableAddress: 0x3f737020,
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, &ep); err != nil {
		t.Fatalf("encode entry point: %v", err)
	}
	b := buf.Bytes()
	var sum uint8
	for _, c := range b {
		sum += c
	}
	b[5] = 0 - sum // 8-bit sum-to-zero checksum
	copy(region, b)
	return region
}

func sampleRegion(t *testing.T) []byte {
	t.Helper()
	uuid := [16]byte{
		0x2e, 0xd6, 0xcc, 0x84, 0x87, 0xe3, 0x46, 0x79,
		0xb1, 0x41, 0xe0, 0x00, 0x52, 0x50, 0x69, 0x00,
	}
	tables := append(
		type1(uuid, "Raspberry Pi", "Raspberry Pi 5 Model B Rev 1.1", "1.1",
			"79a6e38784ccd62e", "E04171", "Raspberry Pi"),
		endOfTable()...)
	return buildRegion(t, tables)
}

func TestParseSystemInformation(t *testing.T) {
	info, err := Parse(sampleRegion(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, tc := range []struct{ name, got, want string }{
		{"Manufacturer", info.Manufacturer, "Raspberry Pi"},
		{"Product", info.Product, "Raspberry Pi 5 Model B Rev 1.1"},
		{"Version", info.Version, "1.1"},
		{"Serial", info.Serial, "79a6e38784ccd62e"},
		{"SKU", info.SKU, "E04171"},
		{"Family", info.Family, "Raspberry Pi"},
		{"SMBIOSVersion", info.SMBIOSVersion, "3.7.0"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// The UUID is the whole point of reading SMBIOS: it exists nowhere in
	// the U-Boot environment.
	if info.UUID == "" {
		t.Error("UUID is empty")
	}
}

// A blank EEPROM must report ErrNoTables so callers fall back to the env.
func TestParseBlankRegion(t *testing.T) {
	if _, err := Parse(make([]byte, testRegionSize)); !errors.Is(err, ErrNoTables) {
		t.Errorf("Parse(blank) = %v, want ErrNoTables", err)
	}
	if _, err := Parse(nil); !errors.Is(err, ErrNoTables) {
		t.Errorf("Parse(nil) = %v, want ErrNoTables", err)
	}
}

func TestParseRejectsBadChecksum(t *testing.T) {
	region := sampleRegion(t)
	region[5]++ // corrupt the entry point checksum
	if _, err := Parse(region); err == nil || errors.Is(err, ErrNoTables) {
		t.Errorf("Parse(bad checksum) = %v, want a checksum error", err)
	}
}

// memBackend serves a whole chip; the store must read only its region.
type memBackend struct{ buf []byte }

func (m *memBackend) Size() int { return len(m.buf) }

func (m *memBackend) ReadAt(off int, p []byte) error {
	if off < 0 || off+len(p) > len(m.buf) {
		return fmt.Errorf("read [%d,%d) out of range", off, off+len(p))
	}
	copy(p, m.buf[off:])
	return nil
}

// The store must read at CONFIG_SMBIOS_I2C_STORE_OFFSET, not offset 0 (which
// holds the UEFI variable blob).
func TestStoreReadsAtOffset(t *testing.T) {
	chip := &memBackend{buf: make([]byte, testChipSize)}
	// Seed the UEFI + env regions with noise the store must not parse.
	for i := range testOffset {
		chip.buf[i] = 0xAA
	}
	copy(chip.buf[testOffset:], sampleRegion(t))

	s := NewStore(chip, testOffset, testRegionSize)
	if !s.Available() {
		t.Fatal("store reports unavailable")
	}
	info, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if info.Product != "Raspberry Pi 5 Model B Rev 1.1" {
		t.Errorf("Product = %q", info.Product)
	}
}

func TestStoreBlankAndUnconfigured(t *testing.T) {
	blank := NewStore(&memBackend{buf: make([]byte, testChipSize)}, testOffset, testRegionSize)
	if _, err := blank.Load(); !errors.Is(err, ErrNoTables) {
		t.Errorf("Load(blank) = %v, want ErrNoTables", err)
	}

	var nilStore *Store
	if nilStore.Available() {
		t.Error("nil store reports Available")
	}
	if _, err := nilStore.Load(); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Load(nil) = %v, want ErrNotConfigured", err)
	}
}
