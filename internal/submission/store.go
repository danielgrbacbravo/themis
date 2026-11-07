package submission

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

type Record struct {
	Receipt Receipt `json:"receipt"`
	Result  *Result `json:"result,omitempty"`
}
type Store struct {
	Path string
	mu   sync.Mutex
}
type journal struct {
	Version int               `json:"version"`
	Records map[string]Record `json:"records"`
}

func DefaultStorePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "themis", "submissions.json")
}
func (s *Store) access(write bool, edit func(*journal) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	j := journal{Version: 1, Records: map[string]Record{}}
	if data, e := os.ReadFile(s.Path); e == nil {
		if e = json.Unmarshal(data, &j); e != nil {
			return fmt.Errorf("read submission history: %w", e)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if j.Version != 1 {
		return fmt.Errorf("unsupported submission history version")
	}
	if j.Records == nil {
		j.Records = map[string]Record{}
	}
	if err = edit(&j); err != nil {
		return err
	}
	if !write {
		return nil
	}
	temp, err := os.CreateTemp(filepath.Dir(s.Path), ".submissions-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err = json.NewEncoder(temp).Encode(j); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp.Name(), s.Path)
}
func (s *Store) SaveReceipt(r Receipt) error {
	return s.access(true, func(j *journal) error {
		key := r.SubmissionURL
		if key == "" {
			key = r.AssignmentURL + "#unknown-" + r.CreatedAt.Format(time.RFC3339Nano)
		}
		if r.SubmissionURL != "" {
			delete(j.Records, r.AssignmentURL+"#unknown-"+r.CreatedAt.Format(time.RFC3339Nano))
		}
		record := j.Records[key]
		record.Receipt = r
		j.Records[key] = record
		return nil
	})
}
func (s *Store) SaveResult(r Result) error {
	return s.access(true, func(j *journal) error {
		record := j.Records[r.SubmissionURL]
		if record.Receipt.SubmissionURL == "" {
			record.Receipt = Receipt{AssignmentURL: r.AssignmentURL, SubmissionURL: r.SubmissionURL, StatusURL: statsURL(r.AssignmentURL), State: "accepted", CreatedAt: r.UpdatedAt}
		}
		record.Result = &r
		j.Records[r.SubmissionURL] = record
		return nil
	})
}
func (s *Store) Latest(assignment string) (Record, bool, error) {
	var records []Record
	err := s.access(false, func(j *journal) error {
		for _, r := range j.Records {
			if r.Receipt.AssignmentURL == publicURL(assignment) {
				records = append(records, r)
			}
		}
		return nil
	})
	if err != nil {
		return Record{}, false, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Receipt.CreatedAt.After(records[j].Receipt.CreatedAt) })
	if len(records) == 0 {
		return Record{}, false, nil
	}
	return records[0], true, nil
}
