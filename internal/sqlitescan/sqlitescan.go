// Package sqlitescan is a read-only scanner for SQLite database files.
//
// It exists so youtuimusic can read Chromium's cookie database without
// linking a SQLite engine. modernc.org/sqlite — the cgo-free option — costs
// 7.7 MB of binary and 50 transitive modules, measured, to run one query at
// startup. The file format itself is public and stable, and the part we need
// is a table scan, so this reads it directly.
//
// It is deliberately not a database: no SQL, no indexes, no writes. Table
// walks one table's b-tree and returns every row, following overflow pages
// and applying any uncheckpointed write-ahead log.
package sqlitescan

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	fileHeaderSize = 100
	magic          = "SQLite format 3\x00"
)

// DB is an open database file, held in memory.
type DB struct {
	data     []byte
	pageSize int
	reserved int
	enc      uint32
	// wal holds pages superseded by an uncheckpointed write-ahead log.
	wal map[uint32][]byte
}

// Table is one table's contents.
type Table struct {
	Columns []string
	Rows    [][]any
}

// Column returns the value of a named column in a row, and whether the
// column exists. Names are matched case-insensitively, as SQLite does.
func (t *Table) Column(row []any, name string) (any, bool) {
	for i, c := range t.Columns {
		if strings.EqualFold(c, name) && i < len(row) {
			return row[i], true
		}
	}
	return nil, false
}

// Open reads a database file. The whole file is loaded: cookie databases are
// a few megabytes, and it makes overlaying the write-ahead log trivial.
func Open(path string) (*DB, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < fileHeaderSize || string(data[:16]) != magic {
		return nil, fmt.Errorf("sqlitescan: %s: not a SQLite database", path)
	}
	pageSize := int(binary.BigEndian.Uint16(data[16:18]))
	if pageSize == 1 {
		pageSize = 65536 // the one value too large for the field
	}
	if pageSize < 512 || pageSize&(pageSize-1) != 0 {
		return nil, fmt.Errorf("sqlitescan: %s: bad page size %d", path, pageSize)
	}
	db := &DB{
		data:     data,
		pageSize: pageSize,
		reserved: int(data[20]),
		enc:      binary.BigEndian.Uint32(data[56:60]),
	}
	if db.reserved >= pageSize {
		return nil, fmt.Errorf("sqlitescan: %s: reserved space %d exceeds page", path, db.reserved)
	}
	if err := db.loadWAL(path + "-wal"); err != nil {
		return nil, err
	}
	return db, nil
}

// usable is the page size minus any per-page reserved region.
func (d *DB) usable() int { return d.pageSize - d.reserved }

func (d *DB) page(n uint32) ([]byte, error) {
	if n == 0 {
		return nil, fmt.Errorf("sqlitescan: page 0 requested")
	}
	if p, ok := d.wal[n]; ok {
		return p, nil
	}
	off := (int(n) - 1) * d.pageSize
	if off < 0 || off+d.pageSize > len(d.data) {
		return nil, fmt.Errorf("sqlitescan: page %d past end of file", n)
	}
	return d.data[off : off+d.pageSize], nil
}

// Table returns every row of a table.
func (d *DB) Table(name string) (tbl *Table, err error) {
	// The input is a file another program writes and may have truncated mid
	// write. Every read below is bounds-checked, but a parser over untrusted
	// bytes gets a backstop so a malformed page is an error, not a crash.
	defer func() {
		if r := recover(); r != nil {
			tbl, err = nil, fmt.Errorf("sqlitescan: malformed database: %v", r)
		}
	}()

	schema, err := d.scan(1)
	if err != nil {
		return nil, fmt.Errorf("sqlitescan: schema: %w", err)
	}
	for _, r := range schema {
		// sqlite_master is (type, name, tbl_name, rootpage, sql).
		if len(r.vals) < 5 {
			continue
		}
		if typ, _ := r.vals[0].(string); typ != "table" {
			continue
		}
		if nm, _ := r.vals[1].(string); !strings.EqualFold(nm, name) {
			continue
		}
		root, ok := r.vals[3].(int64)
		if !ok || root <= 0 {
			return nil, fmt.Errorf("sqlitescan: table %q has no root page", name)
		}
		sql, _ := r.vals[4].(string)
		cols, rowidCol := parseColumns(sql)
		rows, err := d.scan(uint32(root))
		if err != nil {
			return nil, fmt.Errorf("sqlitescan: table %q: %w", name, err)
		}
		out := make([][]any, 0, len(rows))
		for _, r := range rows {
			vals := r.vals
			// An INTEGER PRIMARY KEY column aliases the rowid and is stored
			// as NULL; the real value only exists in the cell header.
			if rowidCol >= 0 && rowidCol < len(vals) && vals[rowidCol] == nil {
				vals[rowidCol] = r.rowid
			}
			out = append(out, vals)
		}
		return &Table{Columns: cols, Rows: out}, nil
	}
	return nil, fmt.Errorf("sqlitescan: no table named %q", name)
}

type record struct {
	rowid int64
	vals  []any
}

// scan walks a table b-tree from its root page, in rowid order.
func (d *DB) scan(root uint32) ([]record, error) {
	var out []record
	seen := make(map[uint32]bool)

	var walk func(uint32) error
	walk = func(n uint32) error {
		if seen[n] {
			return fmt.Errorf("page %d visited twice — cyclic b-tree", n)
		}
		seen[n] = true

		pg, err := d.page(n)
		if err != nil {
			return err
		}
		// Page 1 carries the file header ahead of its b-tree header.
		off := 0
		if n == 1 {
			off = fileHeaderSize
		}
		if off+8 > len(pg) {
			return fmt.Errorf("page %d truncated", n)
		}
		kind := pg[off]
		cells := int(binary.BigEndian.Uint16(pg[off+3 : off+5]))

		switch kind {
		case 0x0d: // leaf table page: the rows live here
			base := off + 8
			for i := 0; i < cells; i++ {
				at, err := cellOffset(pg, base, i, d.usable())
				if err != nil {
					return fmt.Errorf("page %d: %w", n, err)
				}
				payload, rowid, err := d.leafCell(pg, at)
				if err != nil {
					return fmt.Errorf("page %d cell %d: %w", n, i, err)
				}
				vals, err := parseRecord(payload, d.enc)
				if err != nil {
					return fmt.Errorf("page %d cell %d: %w", n, i, err)
				}
				out = append(out, record{rowid: rowid, vals: vals})
			}
			return nil

		case 0x05: // interior table page: pointers to children
			base := off + 12
			for i := 0; i < cells; i++ {
				at, err := cellOffset(pg, base, i, d.usable())
				if err != nil {
					return fmt.Errorf("page %d: %w", n, err)
				}
				if at+4 > len(pg) {
					return fmt.Errorf("page %d: child pointer past end", n)
				}
				if err := walk(binary.BigEndian.Uint32(pg[at : at+4])); err != nil {
					return err
				}
			}
			return walk(binary.BigEndian.Uint32(pg[off+8 : off+12]))

		default:
			return fmt.Errorf("page %d is not a table page (type 0x%02x)", n, kind)
		}
	}

	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

// cellOffset reads the nth entry of a page's cell pointer array.
func cellOffset(pg []byte, base, i, usable int) (int, error) {
	p := base + i*2
	if p+2 > len(pg) {
		return 0, fmt.Errorf("cell pointer %d past end of page", i)
	}
	at := int(binary.BigEndian.Uint16(pg[p : p+2]))
	if at < 0 || at >= usable {
		return 0, fmt.Errorf("cell %d points outside the page (%d)", i, at)
	}
	return at, nil
}

// leafCell returns a cell's full payload, following overflow pages, and its
// rowid.
func (d *DB) leafCell(pg []byte, at int) ([]byte, int64, error) {
	size, n1 := uvarint(pg[at:])
	if n1 == 0 {
		return nil, 0, fmt.Errorf("bad payload length")
	}
	rowid, n2 := varint(pg[at+n1:])
	if n2 == 0 {
		return nil, 0, fmt.Errorf("bad rowid")
	}
	body := pg[at+n1+n2:]

	usable := d.usable()
	maxLocal := usable - 35
	if size <= uint64(maxLocal) {
		if uint64(len(body)) < size {
			return nil, 0, fmt.Errorf("payload of %d runs past the page", size)
		}
		return body[:size], rowid, nil
	}

	// Spilled. The amount kept on this page is chosen so that overflow pages
	// stay as full as possible; the formula is part of the format.
	minLocal := ((usable-12)*32)/255 - 23
	local := minLocal + (int(size)-minLocal)%(usable-4)
	if local > maxLocal {
		local = minLocal
	}
	if local < 0 || local+4 > len(body) {
		return nil, 0, fmt.Errorf("spilled payload does not fit the page")
	}
	buf := make([]byte, 0, size)
	buf = append(buf, body[:local]...)
	next := binary.BigEndian.Uint32(body[local : local+4])

	for next != 0 && uint64(len(buf)) < size {
		opg, err := d.page(next)
		if err != nil {
			return nil, 0, err
		}
		if usable > len(opg) {
			return nil, 0, fmt.Errorf("overflow page %d truncated", next)
		}
		next = binary.BigEndian.Uint32(opg[:4])
		chunk := opg[4:usable]
		if room := int(size) - len(buf); len(chunk) > room {
			chunk = chunk[:room]
		}
		buf = append(buf, chunk...)
	}
	if uint64(len(buf)) != size {
		return nil, 0, fmt.Errorf("overflow chain ended %d bytes short", size-uint64(len(buf)))
	}
	return buf, rowid, nil
}

// parseRecord decodes one row: a header of serial types, then the values.
func parseRecord(rec []byte, enc uint32) ([]any, error) {
	hdrLen, n := uvarint(rec)
	if n == 0 || hdrLen < uint64(n) || hdrLen > uint64(len(rec)) {
		return nil, fmt.Errorf("bad record header")
	}
	var types []uint64
	for p := n; p < int(hdrLen); {
		t, m := uvarint(rec[p:])
		if m == 0 {
			return nil, fmt.Errorf("bad serial type")
		}
		types = append(types, t)
		p += m
	}

	body := rec[hdrLen:]
	vals := make([]any, 0, len(types))
	for _, t := range types {
		size, err := serialSize(t)
		if err != nil {
			return nil, err
		}
		if size > len(body) {
			return nil, fmt.Errorf("value of %d bytes runs past the record", size)
		}
		vals = append(vals, decodeValue(t, body[:size], enc))
		body = body[size:]
	}
	return vals, nil
}

func serialSize(t uint64) (int, error) {
	switch {
	case t == 0, t == 8, t == 9:
		return 0, nil // NULL, and the constants 0 and 1
	case t <= 4:
		return int(t), nil
	case t == 5:
		return 6, nil
	case t == 6, t == 7:
		return 8, nil
	case t == 10, t == 11:
		return 0, fmt.Errorf("reserved serial type %d", t)
	default:
		return int((t - 12) / 2), nil
	}
}

func decodeValue(t uint64, b []byte, enc uint32) any {
	switch {
	case t == 0:
		return nil
	case t == 8:
		return int64(0)
	case t == 9:
		return int64(1)
	case t <= 6:
		// Big-endian two's complement of the stored width.
		var v int64
		if len(b) > 0 && b[0]&0x80 != 0 {
			v = -1 // sign-extend
		}
		for _, c := range b {
			v = v<<8 | int64(c)
		}
		return v
	case t == 7:
		var bits uint64
		for _, c := range b {
			bits = bits<<8 | uint64(c)
		}
		return math.Float64frombits(bits)
	case t%2 == 0:
		out := make([]byte, len(b))
		copy(out, b)
		return out
	default:
		return decodeText(b, enc)
	}
}

func decodeText(b []byte, enc uint32) string {
	switch enc {
	case 2, 3: // UTF-16, little- and big-endian
		if len(b)%2 != 0 {
			b = b[:len(b)-1]
		}
		units := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if enc == 2 {
				units = append(units, binary.LittleEndian.Uint16(b[i:i+2]))
			} else {
				units = append(units, binary.BigEndian.Uint16(b[i:i+2]))
			}
		}
		return string(utf16.Decode(units))
	default:
		if !utf8.Valid(b) {
			return strings.ToValidUTF8(string(b), "�")
		}
		return string(b)
	}
}

// varint reads SQLite's big-endian variable-length integer: up to nine
// bytes, seven bits each, with the ninth contributing all eight. It returns
// the value and the bytes consumed, or 0 bytes if the input is short.
func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < 8; i++ {
		if i >= len(b) {
			return 0, 0
		}
		v = v<<7 | uint64(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	if len(b) < 9 {
		return 0, 0
	}
	return v<<8 | uint64(b[8]), 9
}

func varint(b []byte) (int64, int) {
	v, n := uvarint(b)
	return int64(v), n
}
