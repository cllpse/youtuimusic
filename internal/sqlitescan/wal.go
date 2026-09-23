package sqlitescan

import (
	"encoding/binary"
	"fmt"
	"os"
)

// loadWAL applies an uncheckpointed write-ahead log over the main file.
//
// Ignoring it would be the quiet kind of wrong: Chromium commits to the WAL
// and checkpoints later, so a freshly refreshed session cookie can live only
// there. Reading the main file alone would return the superseded value and
// look like a stale login.
func (d *DB) loadWAL(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) < walHeaderSize {
		return nil // an empty log, which is the state right after a checkpoint
	}

	magic := binary.BigEndian.Uint32(data[0:4])
	if magic != 0x377f0682 && magic != 0x377f0683 {
		return fmt.Errorf("sqlitescan: %s: not a write-ahead log", path)
	}
	// The low bit of the magic selects the byte order the checksums use:
	// SQLite writes the magic as WAL_MAGIC | SQLITE_BIGENDIAN, so the bit is
	// set only on a big-endian writer. Verified against a real log.
	bigEndian := magic&1 == 1
	pageSize := int(binary.BigEndian.Uint32(data[8:12]))
	if pageSize != d.pageSize {
		return fmt.Errorf("sqlitescan: %s: page size %d does not match the database's %d",
			path, pageSize, d.pageSize)
	}
	salt := data[16:24]
	s0, s1 := checksum(0, 0, data[0:24], bigEndian)
	if s0 != binary.BigEndian.Uint32(data[24:28]) || s1 != binary.BigEndian.Uint32(data[28:32]) {
		return fmt.Errorf("sqlitescan: %s: header checksum mismatch", path)
	}

	pages := make(map[uint32][]byte)
	pending := make(map[uint32][]byte)
	frameSize := walFrameHeaderSize + pageSize

	for off := walHeaderSize; off+frameSize <= len(data); off += frameSize {
		hdr := data[off : off+walFrameHeaderSize]
		body := data[off+walFrameHeaderSize : off+frameSize]

		// A frame written by an earlier log generation is where this one ends.
		if string(hdr[8:16]) != string(salt) {
			break
		}
		n0, n1 := checksum(s0, s1, hdr[0:8], bigEndian)
		n0, n1 = checksum(n0, n1, body, bigEndian)
		if n0 != binary.BigEndian.Uint32(hdr[16:20]) || n1 != binary.BigEndian.Uint32(hdr[20:24]) {
			break // a torn write: everything from here on is unreliable
		}
		s0, s1 = n0, n1

		pending[binary.BigEndian.Uint32(hdr[0:4])] = body
		// Only a commit frame makes the preceding frames visible.
		if binary.BigEndian.Uint32(hdr[4:8]) != 0 {
			for n, p := range pending {
				pages[n] = p
			}
			pending = make(map[uint32][]byte)
		}
	}

	if len(pages) > 0 {
		d.wal = pages
	}
	return nil
}

const (
	walHeaderSize      = 32
	walFrameHeaderSize = 24
)

// checksum is the WAL's rolling checksum: two accumulators fed pairs of
// 32-bit words. The input is always a multiple of eight bytes.
func checksum(s0, s1 uint32, b []byte, bigEndian bool) (uint32, uint32) {
	order := binary.ByteOrder(binary.LittleEndian)
	if bigEndian {
		order = binary.BigEndian
	}
	for i := 0; i+8 <= len(b); i += 8 {
		s0 += order.Uint32(b[i:i+4]) + s1
		s1 += order.Uint32(b[i+4:i+8]) + s0
	}
	return s0, s1
}
