// Package resumestore is a file-backed implementation of the core walletload
// ResumeState and InFlightJournal ports. It persists the row-key → record-id
// map as a two-column CSV and the in-flight batch journal as JSON lines beside
// it, so an interrupted load resumes without creating duplicates.
package resumestore

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	core "github.com/dpcat237/bbwalet-utils/internal/core/walletload"
)

const (
	filePerm = 0o600
	dirPerm  = 0o750
)

// Store persists load progress to two sibling files.
type Store struct {
	createdPath string
	journalPath string
	created     map[string]string // rowKey -> recordID
	order       []string          // rowKeys in commit order
}

// New loads any existing progress at createdPath (and its ".inflight" journal)
// and returns a ready Store. Missing files are not an error.
func New(createdPath string) (*Store, error) {
	s := &Store{
		createdPath: createdPath,
		journalPath: createdPath + ".inflight",
		created:     map[string]string{},
	}
	if err := s.loadCreated(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) loadCreated() error {
	data, err := os.ReadFile(s.createdPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading created-records file: %w", err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil {
		return fmt.Errorf("parsing created-records file: %w", err)
	}
	for _, rec := range records {
		if len(rec) != 2 {
			continue
		}
		if _, seen := s.created[rec[0]]; !seen {
			s.order = append(s.order, rec[0])
		}
		s.created[rec[0]] = rec[1]
	}
	return nil
}

// Loaded reports whether rowKey has already been written.
func (s *Store) Loaded(rowKey string) bool {
	_, ok := s.created[rowKey]
	return ok
}

// Commit records that rowKey was written as recordID.
func (s *Store) Commit(rowKey, recordID string) error {
	if err := ensureDir(s.createdPath); err != nil {
		return err
	}
	var line bytes.Buffer
	w := csv.NewWriter(&line)
	if err := w.Write([]string{rowKey, recordID}); err != nil {
		return fmt.Errorf("encoding created record: %w", err)
	}
	w.Flush()
	if err := appendFile(s.createdPath, line.Bytes()); err != nil {
		return err
	}
	if _, seen := s.created[rowKey]; !seen {
		s.order = append(s.order, rowKey)
	}
	s.created[rowKey] = recordID
	return nil
}

// CreatedIDs returns every record id this store has committed, in commit order.
func (s *Store) CreatedIDs() ([]string, error) {
	out := make([]string, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.created[k])
	}
	return out, nil
}

// MarkInFlight appends a batch to the journal before its create call.
func (s *Store) MarkInFlight(b core.Batch) error {
	if err := ensureDir(s.journalPath); err != nil {
		return err
	}
	line, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("encoding in-flight batch: %w", err)
	}
	return appendFile(s.journalPath, append(line, '\n'))
}

// ClearInFlight removes a batch from the journal once it is resolved.
func (s *Store) ClearInFlight(batchID string) error {
	batches, err := s.InFlight()
	if err != nil {
		return err
	}
	kept := batches[:0]
	for _, b := range batches {
		if b.ID != batchID {
			kept = append(kept, b)
		}
	}
	return s.rewriteJournal(kept)
}

// InFlight returns every batch still recorded in the journal.
func (s *Store) InFlight() ([]core.Batch, error) {
	data, err := os.ReadFile(s.journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading in-flight journal: %w", err)
	}
	var out []core.Batch
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var b core.Batch
		if err := json.Unmarshal([]byte(line), &b); err != nil {
			return nil, fmt.Errorf("decoding in-flight batch: %w", err)
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *Store) rewriteJournal(batches []core.Batch) error {
	if len(batches) == 0 {
		if err := os.Remove(s.journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing in-flight journal: %w", err)
		}
		return nil
	}
	var buf bytes.Buffer
	for _, b := range batches {
		line, err := json.Marshal(b)
		if err != nil {
			return fmt.Errorf("encoding in-flight batch: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(s.journalPath, buf.Bytes(), filePerm); err != nil {
		return fmt.Errorf("rewriting in-flight journal: %w", err)
	}
	return nil
}

func appendFile(path string, data []byte) error {
	//nolint:gosec // path is the operator-supplied --resume-state location, not attacker input
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("writing %s: %w", filepath.Base(path), werr)
	}
	if cerr != nil {
		return fmt.Errorf("closing %s: %w", filepath.Base(path), cerr)
	}
	return nil
}

func ensureDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	return nil
}
