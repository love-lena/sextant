package shipper

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

// spillover wraps a BoltDB database used as finite local buffer when
// ClickHouse is unreachable. Per-Table buckets hold serialized Row
// payloads keyed by a monotonic 16-byte sequence so drain order is
// FIFO. The Row's JSON-decoded form is re-built lazily; the bucket
// stores the gob-encoded Row struct itself.
type spillover struct {
	db   *bolt.DB
	path string
	// logicalBytes is the sum of value lengths currently stored in the
	// per-Table buckets. We track this in memory because BoltDB never
	// shrinks its file on Delete — `os.Stat(buffer.db).Size()` would
	// stay at the high-water mark after a successful drain, causing
	// `degraded_mode = "drop_oldest"` to spiral (one drop frees zero
	// "bytes" so the next batch trips the cap too) and causing a fail-
	// closed restart to re-trip on the first transient ClickHouse
	// error. logicalBytes is incremented on Put, decremented on
	// Delete and DropOldest, and rebuilt on openSpillover by scanning
	// every bucket once.
	logicalBytes atomic.Int64
}

// openSpillover opens (or creates) the BoltDB at dir/buffer.db. Creates
// the per-Table buckets and rebuilds logicalBytes from a single
// read-only scan of the buckets so a previous process's writes survive
// restart accounting.
func openSpillover(dir string) (*spillover, error) {
	if dir == "" {
		return nil, fmt.Errorf("shipper: spillover dir is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("shipper: mkdir spillover dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "buffer.db")
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("shipper: open bolt %s: %w", path, err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, tbl := range AllTables() {
			if _, err := tx.CreateBucketIfNotExists(bucketName(tbl)); err != nil {
				return fmt.Errorf("create bucket %s: %w", tbl, err)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("shipper: init buckets: %w", err)
	}
	s := &spillover{db: db, path: path}
	if err := s.rebuildLogicalBytes(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the BoltDB file lock.
func (s *spillover) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path returns the on-disk file path.
func (s *spillover) Path() string { return s.path }

// SizeBytes returns the logical size of buffered data — the sum of
// value lengths currently stored in the per-Table buckets. This is
// NOT the on-disk file size: BoltDB never shrinks its file on Delete,
// so the file size is a high-water mark that does not reflect drain
// progress. The hard-cap and metrics path both want the live logical
// number; that's what we return here.
func (s *spillover) SizeBytes() int64 {
	return s.logicalBytes.Load()
}

// rebuildLogicalBytes walks every bucket and recomputes logicalBytes
// from scratch. Called once on open so a previous process's writes
// (which we still need to drain) contribute to the cap accounting.
func (s *spillover) rebuildLogicalBytes() error {
	var total int64
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, tbl := range AllTables() {
			b := tx.Bucket(bucketName(tbl))
			if b == nil {
				continue
			}
			c := b.Cursor()
			for k, v := c.First(); k != nil; k, v = c.Next() {
				total += int64(len(v))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("shipper: spillover rebuild logical bytes: %w", err)
	}
	s.logicalBytes.Store(total)
	return nil
}

// Put appends the supplied rows to their respective per-Table buckets
// in a single Bolt transaction. Returns the number of bytes written
// (post-encoding, summed across all rows) and adds that figure to the
// logical-bytes counter.
//
// Rows of mixed tables in one Put are allowed; callers normally pass
// one Table per call, but tests exercise the multi-table form to verify
// no cross-bucket bleed.
func (s *spillover) Put(rows []Row) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	var written int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		for _, r := range rows {
			b := tx.Bucket(bucketName(r.Table))
			if b == nil {
				return fmt.Errorf("missing bucket for table %s", r.Table)
			}
			seq, err := b.NextSequence()
			if err != nil {
				return fmt.Errorf("next sequence: %w", err)
			}
			key := buildKey(seq)
			data, err := encodeRow(r)
			if err != nil {
				return err
			}
			if err := b.Put(key, data); err != nil {
				return fmt.Errorf("bolt put: %w", err)
			}
			written += int64(len(data))
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("shipper: spillover put: %w", err)
	}
	s.logicalBytes.Add(written)
	return written, nil
}

// PeekBatch returns up to max rows from bucketName(table) in FIFO key
// order along with their bucket keys. Callers should drain (delete)
// keys after the rows have been durably written.
func (s *spillover) PeekBatch(table Table, max int) ([][]byte, []Row, error) {
	if max <= 0 {
		return nil, nil, nil
	}
	var keys [][]byte
	var rows []Row
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName(table))
		if b == nil {
			return fmt.Errorf("missing bucket for table %s", table)
		}
		c := b.Cursor()
		for k, v := c.First(); k != nil && len(rows) < max; k, v = c.Next() {
			// Copy because Bolt's slices become invalid after the
			// transaction ends.
			kc := make([]byte, len(k))
			copy(kc, k)
			row, err := decodeRow(v)
			if err != nil {
				return fmt.Errorf("decode row at key %x: %w", k, err)
			}
			keys = append(keys, kc)
			rows = append(rows, row)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("shipper: spillover peek %s: %w", table, err)
	}
	return keys, rows, nil
}

// Delete removes the supplied keys from bucketName(table). Used by the
// drain loop after a successful ClickHouse insert. Reads each value's
// length inside the same transaction before deletion so we can credit
// the logical-bytes counter accurately.
func (s *spillover) Delete(table Table, keys [][]byte) error {
	if len(keys) == 0 {
		return nil
	}
	var freed int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketName(table))
		if b == nil {
			return fmt.Errorf("missing bucket for table %s", table)
		}
		for _, k := range keys {
			if v := b.Get(k); v != nil {
				freed += int64(len(v))
			}
			if err := b.Delete(k); err != nil {
				return fmt.Errorf("delete %x: %w", k, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("shipper: spillover delete %s: %w", table, err)
	}
	if freed > 0 {
		s.logicalBytes.Add(-freed)
	}
	return nil
}

// CountAll returns the number of rows currently in each per-table
// bucket. Used by the metrics goroutine and by tests.
func (s *spillover) CountAll() (map[Table]int, error) {
	out := make(map[Table]int, len(AllTables()))
	err := s.db.View(func(tx *bolt.Tx) error {
		for _, tbl := range AllTables() {
			b := tx.Bucket(bucketName(tbl))
			if b == nil {
				continue
			}
			out[tbl] = b.Stats().KeyN
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("shipper: spillover count: %w", err)
	}
	return out, nil
}

// DropOldest removes up to count rows from the oldest entries across
// every bucket, returning the count actually dropped. Used by
// degraded_mode = "drop_oldest" to free space. Updates the logical-
// bytes counter with the freed value lengths.
func (s *spillover) DropOldest(count int) (int, error) {
	if count <= 0 {
		return 0, nil
	}
	dropped := 0
	var freed int64
	err := s.db.Update(func(tx *bolt.Tx) error {
		for _, tbl := range AllTables() {
			if dropped >= count {
				return nil
			}
			b := tx.Bucket(bucketName(tbl))
			if b == nil {
				continue
			}
			c := b.Cursor()
			for k, v := c.First(); k != nil && dropped < count; k, v = c.Next() {
				freed += int64(len(v))
				if err := b.Delete(k); err != nil {
					return err
				}
				dropped++
			}
		}
		return nil
	})
	if err != nil {
		return dropped, fmt.Errorf("shipper: drop_oldest: %w", err)
	}
	if freed > 0 {
		s.logicalBytes.Add(-freed)
	}
	return dropped, nil
}

// bucketName maps a Table to its BoltDB bucket name.
func bucketName(t Table) []byte {
	return []byte("pending_" + string(t))
}

// buildKey returns the 16-byte FIFO key: 8 bytes of now-nanos then the
// 8-byte bucket sequence. Bolt iteration is byte-lex, so this ordering
// puts earlier writes first.
func buildKey(seq uint64) []byte {
	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[:8], uint64(time.Now().UTC().UnixNano())) //nolint:gosec // monotonic upper bytes need full range
	binary.BigEndian.PutUint64(buf[8:], seq)
	return buf
}

// encodeRow serializes a Row via encoding/gob.
func encodeRow(r Row) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(r); err != nil {
		return nil, fmt.Errorf("encode row: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeRow is the inverse of encodeRow.
func decodeRow(data []byte) (Row, error) {
	var r Row
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&r); err != nil {
		return Row{}, fmt.Errorf("decode row: %w", err)
	}
	return r, nil
}
