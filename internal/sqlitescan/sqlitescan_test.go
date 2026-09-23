package sqlitescan

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// These tests check the reader against the real thing: every fixture is
// built by the sqlite3 CLI, and the expected rows come out of sqlite3 too.
// A format parser that is only tested against its own assumptions proves
// nothing, so nothing here is hand-written except the SQL.

// walFixture leaves a database in the state a running browser leaves it in:
// written to in WAL mode, with the log not yet checkpointed. SQLite
// checkpoints when the last connection closes, so the files are copied out
// while a connection is still holding them open.
func walFixture(t *testing.T, setup, update string) string {
	t.Helper()
	bin := sqlite3Path(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")

	cmd := exec.Command(bin, src)
	cmd.Stdin = strings.NewReader(setup)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 setup: %v\n%s", err, out)
	}

	live := exec.Command(bin, src)
	in, err := live.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := live.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := live.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = in.Close()
		_ = live.Wait()
	}()

	if _, err := io.WriteString(in, "PRAGMA journal_mode=WAL;\nPRAGMA wal_autocheckpoint=0;\n"+
		update+"\n.print COMMITTED\n"); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(out)
	for scan.Scan() && scan.Text() != "COMMITTED" {
	}

	// Copy while that connection is open, so the log survives.
	dst := filepath.Join(dir, "copy.db")
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(src + suffix)
		if err != nil {
			t.Fatalf("reading %s: %v", src+suffix, err)
		}
		if err := os.WriteFile(dst+suffix, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if st, err := os.Stat(dst + "-wal"); err != nil || st.Size() <= walHeaderSize {
		t.Skipf("no uncheckpointed log to test against (%v)", err)
	}
	return dst
}

func sqlite3Path(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 not on PATH; cannot build fixtures")
	}
	return p
}

// build runs SQL against a fresh database and returns its path.
func build(t *testing.T, sql string) string {
	t.Helper()
	bin := sqlite3Path(t)
	path := filepath.Join(t.TempDir(), "test.db")
	cmd := exec.Command(bin, path)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v\n%s", err, out)
	}
	return path
}

// expected asks sqlite3 for the same rows, as SQL literals.
func expected(t *testing.T, path, query string) []string {
	t.Helper()
	cmd := exec.Command(sqlite3Path(t), path)
	cmd.Stdin = strings.NewReader(".mode quote\n" + query + ";\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sqlite3 query: %v\n%s", err, out)
	}
	var rows []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			rows = append(rows, line)
		}
	}
	return rows
}

// literal renders a scanned value the way sqlite3's quote mode does, so the
// two sides are comparable without either one interpreting the other.
func literal(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		s := strconv.FormatFloat(x, 'g', 15, 64)
		if !strings.ContainsAny(s, ".eEnN") {
			s += ".0"
		}
		return s
	case []byte:
		return fmt.Sprintf("x'%x'", x)
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	default:
		return fmt.Sprintf("<%T>", v)
	}
}

func rowsAsLiterals(tbl *Table) []string {
	out := make([]string, 0, len(tbl.Rows))
	for _, r := range tbl.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			parts[i] = literal(v)
		}
		out = append(out, strings.Join(parts, ","))
	}
	return out
}

// check scans a table and compares it against sqlite3's own answer.
func check(t *testing.T, path, table, query string) *Table {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl, err := db.Table(table)
	if err != nil {
		t.Fatalf("Table(%q): %v", table, err)
	}
	got := rowsAsLiterals(tbl)
	want := expected(t, path, query)
	if len(got) != len(want) {
		t.Fatalf("got %d rows, sqlite3 says %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("row %d differs from sqlite3 (%d vs %d bytes):\n got %.200s\nwant %.200s",
				i, len(got[i]), len(want[i]), got[i], want[i])
		}
	}
	return tbl
}

// The shape that actually matters: Chromium's cookie table.
func TestChromiumShapedTable(t *testing.T) {
	path := build(t, `
CREATE TABLE cookies(
  creation_utc INTEGER NOT NULL,
  host_key TEXT NOT NULL,
  top_frame_site_key TEXT NOT NULL,
  name TEXT NOT NULL,
  value TEXT NOT NULL,
  encrypted_value BLOB NOT NULL,
  path TEXT NOT NULL,
  expires_utc INTEGER NOT NULL,
  is_secure INTEGER NOT NULL,
  is_httponly INTEGER NOT NULL,
  UNIQUE (host_key, top_frame_site_key, name, path));
INSERT INTO cookies VALUES
 (13380000000000,'.youtube.com','','__Secure-3PAPISID','',X'763130AABBCC','/',13500000000000,1,1),
 (13380000000001,'.google.com','','SID','',X'76313100112233','/',13500000000001,1,0),
 (13380000000002,'music.youtube.com','','PREF','pref-value',X'','/',0,0,0);`)

	tbl := check(t, path, "cookies", "SELECT * FROM cookies ORDER BY rowid")

	want := []string{
		"creation_utc", "host_key", "top_frame_site_key", "name", "value",
		"encrypted_value", "path", "expires_utc", "is_secure", "is_httponly",
	}
	if strings.Join(tbl.Columns, ",") != strings.Join(want, ",") {
		t.Fatalf("columns = %v", tbl.Columns)
	}
	// The UNIQUE table constraint must not be mistaken for a column.
	if len(tbl.Columns) != 10 {
		t.Fatalf("got %d columns, want 10", len(tbl.Columns))
	}
	v, ok := tbl.Column(tbl.Rows[0], "HOST_KEY")
	if !ok || v != ".youtube.com" {
		t.Fatalf("Column(host_key) = %v, %v", v, ok)
	}
}

// Every storage class, including the widths that share a serial type range.
func TestAllValueTypes(t *testing.T) {
	path := build(t, `
CREATE TABLE t(a,b,c,d,e,f,g,h,i,j);
INSERT INTO t VALUES(NULL, 0, 1, 127, -128, 32767, -2147483648, 9223372036854775807, 1.5, 'text');
INSERT INTO t VALUES(-1, 255, 256, 65536, 16777216, 4294967296, 1099511627776, -9223372036854775808, -0.25, x'00FF10');
INSERT INTO t VALUES('', x'', 'quote''s inside', 'ünïcödé ✓', 3.0, 0.1, 42, NULL, NULL, NULL);`)
	check(t, path, "t", "SELECT * FROM t ORDER BY rowid")
}

// A value too large for its page spills into a chain of overflow pages.
func TestOverflowPages(t *testing.T) {
	path := build(t, `
CREATE TABLE t(id INTEGER PRIMARY KEY, blob BLOB, note TEXT);
INSERT INTO t VALUES(1, randomblob(100000), 'huge');
INSERT INTO t VALUES(2, randomblob(4000),   'just over one page');
INSERT INTO t VALUES(3, randomblob(3900),   'just under');
INSERT INTO t VALUES(4, x'',                'empty');`)
	tbl := check(t, path, "t", "SELECT * FROM t ORDER BY rowid")

	// The rowid alias is stored as NULL; it has to come back as the rowid.
	if got, _ := tbl.Column(tbl.Rows[0], "id"); got != int64(1) {
		t.Fatalf("INTEGER PRIMARY KEY = %v, want the rowid 1", got)
	}
	if b, _ := tbl.Column(tbl.Rows[0], "blob"); len(b.([]byte)) != 100000 {
		t.Fatalf("overflowed blob came back %d bytes", len(b.([]byte)))
	}
}

// Enough rows to force interior b-tree pages, which are walked recursively.
func TestInteriorPages(t *testing.T) {
	path := build(t, `
CREATE TABLE t(id INTEGER PRIMARY KEY, pad TEXT);
INSERT INTO t(pad) SELECT hex(randomblob(200)) FROM generate_series(1,5000);`)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl, err := db.Table("t")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if len(tbl.Rows) != 5000 {
		t.Fatalf("got %d rows, want 5000", len(tbl.Rows))
	}
	// Rows must come back in rowid order, which is what walking the tree
	// left to right gives.
	for i, r := range tbl.Rows {
		if r[0] != int64(i+1) {
			t.Fatalf("row %d has id %v", i, r[0])
		}
	}
}

// Chromium commits cookies to a write-ahead log and checkpoints later. Rows
// that live only in the log still have to be visible.
func TestUncheckpointedWAL(t *testing.T) {
	path := walFixture(t, `
PRAGMA journal_mode=WAL;
CREATE TABLE cookies(host_key TEXT, name TEXT, encrypted_value BLOB);
INSERT INTO cookies VALUES('.youtube.com','SID',x'763130AA');
PRAGMA wal_checkpoint(TRUNCATE);`, `
UPDATE cookies SET encrypted_value=x'763131BBBBBB' WHERE name='SID';
INSERT INTO cookies VALUES('.google.com','__Secure-3PAPISID',x'763131CCCC');`)

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl, err := db.Table("cookies")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if len(tbl.Rows) != 2 {
		t.Fatalf("got %d rows, want 2 — the log was not applied", len(tbl.Rows))
	}
	got, _ := tbl.Column(tbl.Rows[0], "encrypted_value")
	if fmt.Sprintf("%x", got) != "763131bbbbbb" {
		t.Fatalf("value = %x, want the updated one from the log", got)
	}
}

// Ignoring the log would return the superseded row and look like a stale
// login, so prove the difference is real rather than assumed.
func TestWALActuallyChangesTheAnswer(t *testing.T) {
	path := walFixture(t, `
PRAGMA journal_mode=WAL;
CREATE TABLE t(v TEXT);
INSERT INTO t VALUES('old');
PRAGMA wal_checkpoint(TRUNCATE);`, "UPDATE t SET v='new';")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if db.wal == nil {
		t.Fatal("log was not loaded")
	}
	tbl, err := db.Table("t")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if tbl.Rows[0][0] != "new" {
		t.Fatalf("value = %v, want new", tbl.Rows[0][0])
	}

	// Without the overlay the same file reads 'old' — the bug this avoids.
	db.wal = nil
	stale, err := db.Table("t")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if stale.Rows[0][0] != "old" {
		t.Fatalf("without the log the value was %v; the test proves nothing", stale.Rows[0][0])
	}
}

// A log left by an earlier generation of the database must be ignored
// rather than applied on top of unrelated pages.
func TestStaleLogIsIgnored(t *testing.T) {
	path := walFixture(t, `
PRAGMA journal_mode=WAL;
CREATE TABLE t(v TEXT);
INSERT INTO t VALUES('only');
PRAGMA wal_checkpoint(TRUNCATE);`, "UPDATE t SET v='newer';")

	raw, err := os.ReadFile(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	// Change the frame's salt so it looks like it belongs to another run.
	// The header's own salt is left alone; a mismatch between the two is
	// exactly how SQLite marks where a reused log stops being current.
	raw[walHeaderSize+8] ^= 0xff
	if err := os.WriteFile(path+"-wal", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	tbl, err := db.Table("t")
	if err != nil {
		t.Fatalf("Table: %v", err)
	}
	if tbl.Rows[0][0] != "only" {
		t.Fatalf("value = %v; frames from another generation were applied", tbl.Rows[0][0])
	}
}

func TestNonDefaultPageSize(t *testing.T) {
	for _, size := range []string{"512", "1024", "16384", "65536"} {
		t.Run(size, func(t *testing.T) {
			path := build(t, `
PRAGMA page_size=`+size+`;
CREATE TABLE t(a TEXT, b BLOB);
INSERT INTO t VALUES('one', randomblob(9000));
INSERT INTO t VALUES('two', randomblob(20));`)
			check(t, path, "t", "SELECT * FROM t ORDER BY rowid")
		})
	}
}

func TestMissingTableIsAnError(t *testing.T) {
	path := build(t, "CREATE TABLE t(a);")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.Table("cookies"); err == nil {
		t.Fatal("expected an error for a table that is not there")
	}
}

func TestNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(path, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("expected an error")
	}
}

// A file cut short mid-write must be an error, not a panic.
func TestTruncatedFileIsAnError(t *testing.T) {
	path := build(t, `CREATE TABLE t(a,b); INSERT INTO t VALUES('x', randomblob(50000));`)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []int{fileHeaderSize + 1, len(raw) / 3, len(raw) / 2, len(raw) - 7} {
		short := filepath.Join(t.TempDir(), "short.db")
		if err := os.WriteFile(short, raw[:keep], 0o600); err != nil {
			t.Fatal(err)
		}
		db, err := Open(short)
		if err != nil {
			continue
		}
		if _, err := db.Table("t"); err == nil {
			t.Errorf("truncating to %d bytes was read as a valid table", keep)
		}
	}
}

func TestQuotedColumnNames(t *testing.T) {
	cols, rowid := parseColumns(`CREATE TABLE x ("odd name" TEXT, [brackets] INT, ` +
		"`ticks` BLOB, plain INTEGER PRIMARY KEY, UNIQUE (plain, ticks))")
	want := []string{"odd name", "brackets", "ticks", "plain"}
	if strings.Join(cols, "|") != strings.Join(want, "|") {
		t.Fatalf("columns = %v", cols)
	}
	if rowid != 3 {
		t.Fatalf("rowid column = %d, want 3", rowid)
	}
}

// A column typed TEXT is not the rowid however its constraints read.
func TestOnlyIntegerPrimaryKeyAliasesTheRowid(t *testing.T) {
	if _, rowid := parseColumns(`CREATE TABLE x (a TEXT PRIMARY KEY, b INT)`); rowid != -1 {
		t.Fatalf("TEXT PRIMARY KEY treated as rowid alias (col %d)", rowid)
	}
	if _, rowid := parseColumns(`CREATE TABLE x (a INTEGER NOT NULL PRIMARY KEY)`); rowid != 0 {
		t.Fatalf("rowid column = %d, want 0", rowid)
	}
}

func TestVarint(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want uint64
		n    int
	}{
		{[]byte{0x00}, 0, 1},
		{[]byte{0x7f}, 127, 1},
		{[]byte{0x81, 0x00}, 128, 2},
		{[]byte{0xff, 0x7f}, 16383, 2},
		// Nine bytes: eight of seven bits, then a full eighth-bit byte.
		{[]byte{0x81, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}, 1 << 57, 9},
		{[]byte{0x81}, 0, 0}, // short input
		{nil, 0, 0},
	} {
		got, n := uvarint(tc.in)
		if n != tc.n || (tc.n > 0 && got != tc.want) {
			t.Errorf("uvarint(%x) = %d, %d; want %d, %d", tc.in, got, n, tc.want, tc.n)
		}
	}
}
