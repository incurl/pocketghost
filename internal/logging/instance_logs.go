package logging

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one line of an instance log file. The JSON shape is byte-for-byte
// compatible with the TypeScript `InstanceLogWriter` output, because the
// dashboard parses it directly.
type Entry struct {
	Message string `json:"message"`
	Stream  string `json:"stream"`
	Time    string `json:"time"`
}

// LogStore owns per-instance log files under `<data>/instances/<id>/logs`.
type LogStore struct {
	root     string
	maxBytes int64
	logger   *Logger

	mu    sync.Mutex
	files map[string]*os.File
	subs  map[string]map[int]chan Entry
	next  int
}

// NewLogStore creates a log store rooted at the instances directory.
func NewLogStore(instancesRoot string, maxBytes int64, logger *Logger) *LogStore {
	if maxBytes <= 0 {
		// The legacy implementation used a 1 KiB ceiling which rotated almost
		// constantly. 10 MiB matches the documented intent of that code.
		maxBytes = 10 << 20
	}
	return &LogStore{
		root:     instancesRoot,
		maxBytes: maxBytes,
		logger:   logger,
		files:    map[string]*os.File{},
		subs:     map[string]map[int]chan Entry{},
	}
}

// Dir returns the log directory for an instance.
func (s *LogStore) Dir(instanceID string) string {
	return filepath.Join(s.root, instanceID, "logs")
}

// Path returns the log file for an instance target (for example "exec").
func (s *LogStore) Path(instanceID, target string) string {
	return filepath.Join(s.Dir(instanceID), target+".log")
}

// EnsureDir creates the log directory, matching `ensureInstanceDirectoryStructure`.
func (s *LogStore) EnsureDir(instanceID string) error {
	return os.MkdirAll(s.Dir(instanceID), 0o755)
}

// Writer returns a writer bound to one instance/target.
func (s *LogStore) Writer(instanceID, target string) *LogWriter {
	return &LogWriter{store: s, instanceID: instanceID, target: target}
}

// LogWriter appends structured lines to one instance log file.
type LogWriter struct {
	store      *LogStore
	instanceID string
	target     string
}

// Info appends on the stdout stream.
func (w *LogWriter) Info(msg string) { w.write("stdout", msg) }

// Error appends on the stderr stream.
func (w *LogWriter) Error(msg string) { w.write("stderr", msg) }

func (w *LogWriter) write(stream, msg string) {
	if w == nil || w.store == nil {
		return
	}
	e := Entry{Message: msg, Stream: stream, Time: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := w.store.Append(w.instanceID, w.target, e); err != nil && w.store.logger != nil {
		w.store.logger.Warnf("instance log write failed for %s/%s: %v", w.instanceID, w.target, err)
	}
}

// Append writes one entry, rotating the file when it exceeds maxBytes.
func (s *LogStore) Append(instanceID, target string, e Entry) error {
	if err := s.EnsureDir(instanceID); err != nil {
		return err
	}
	path := s.Path(instanceID, target)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	f, ok := s.files[path]
	if !ok {
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		s.files[path] = f
	}

	if st, statErr := f.Stat(); statErr == nil && st.Size()+int64(len(line)) > s.maxBytes {
		_ = f.Close()
		delete(s.files, path)
		_ = os.Rename(path, path+".1")
		f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		s.files[path] = f
	}

	if _, err := f.Write(line); err != nil {
		return err
	}

	for _, ch := range s.subs[instanceID] {
		select {
		case ch <- e:
		default: // never block the process pipe on a slow UI client
		}
	}
	return nil
}

// Close closes cached file handles.
func (s *LogStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	for path, f := range s.files {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(s.files, path)
	}
	return firstErr
}

// ReadTail returns the last n parsed entries without following the file.
func (s *LogStore) ReadTail(instanceID, target string, n int) ([]Entry, error) {
	if n <= 0 {
		n = 100
	}
	f, err := os.Open(s.Path(instanceID, target))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	entries, err := scanEntries(f)
	if err != nil {
		return nil, err
	}
	if len(entries) > n {
		entries = entries[len(entries)-n:]
	}
	return entries, nil
}

func scanEntries(r io.Reader) ([]Entry, error) {
	out := make([]Entry, 0, 256)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			out = append(out, Entry{Message: string(line), Stream: "stdout", Time: time.Now().UTC().Format(time.RFC3339Nano)})
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Subscribe registers a live subscriber. The returned cancel function must be
// called to avoid leaking the channel.
func (s *LogStore) Subscribe(instanceID string) (<-chan Entry, func()) {
	ch := make(chan Entry, 256)
	s.mu.Lock()
	if s.subs[instanceID] == nil {
		s.subs[instanceID] = map[int]chan Entry{}
	}
	id := s.next
	s.next++
	s.subs[instanceID][id] = ch
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		if m := s.subs[instanceID]; m != nil {
			if c, ok := m[id]; ok {
				delete(m, id)
				close(c)
			}
			if len(m) == 0 {
				delete(s.subs, instanceID)
			}
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

// Follow replays `linesBack` entries and then streams new ones until ctx is done.
// It is the Go replacement for the `tail` NPM package usage in
// InstanceLogReader.tail().
func (s *LogStore) Follow(ctx context.Context, instanceID, target string, linesBack int, out chan<- Entry) error {
	defer close(out)

	ch, cancel := s.Subscribe(instanceID)
	defer cancel()

	back, err := s.ReadTail(instanceID, target, linesBack)
	if err != nil {
		return err
	}
	for _, e := range back {
		select {
		case out <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case e := <-ch:
			select {
			case out <- e:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// EnsureInstanceDirStructure recreates the legacy
// `ensureInstanceDirectoryStructure()` layout: pb_data, pb_migrations, pb_public,
// logs and pb_hooks.
func EnsureInstanceDirStructure(instanceDir string, logger *Logger) error {
	for _, d := range []string{"pb_data", "pb_migrations", "pb_public", "logs", "pb_hooks"} {
		p := filepath.Join(instanceDir, d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", p, err)
		}
	}
	if logger != nil {
		logger.Debugf("ensured instance directory structure at %s", instanceDir)
	}
	return nil
}
