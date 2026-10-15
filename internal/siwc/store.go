package siwc

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session is local-only. Never serialize it to ARTEX's database or browser.
type Session struct {
	HostID       string    `json:"host_id"`
	BridgeKey    string    `json:"bridge_key"`
	ClientID     string    `json:"client_id,omitempty"`
	Subject      string    `json:"subject,omitempty"`
	Email        string    `json:"email,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Scopes       string    `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
}

type Store struct {
	mu   sync.Mutex
	path string
	s    Session
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	st := &Store{path: filepath.Join(dir, "chatgpt-plan.auth")}
	if blob, err := os.ReadFile(st.path); err == nil {
		clear, err := unprotect(blob)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(clear, &st.s); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	changed := false
	if st.s.HostID == "" {
		u, err := randomToken(16)
		if err != nil {
			return nil, err
		}
		st.s.HostID = "urn:uuid:" + uuidFromToken(u)
		changed = true
	}
	if st.s.BridgeKey == "" {
		key, err := randomToken(32)
		if err != nil {
			return nil, err
		}
		st.s.BridgeKey = key
		changed = true
	}
	if changed {
		if err := st.saveLocked(); err != nil {
			return nil, err
		}
	}
	return st, nil
}

func uuidFromToken(s string) string {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const h = "0123456789abcdef"
	buf := make([]byte, 36)
	j := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			buf[j] = '-'
			j++
		}
		buf[j], buf[j+1] = h[v>>4], h[v&15]
		j += 2
	}
	return string(buf)
}

func (st *Store) Snapshot() Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s
}

// Reload lets a long-running bridge observe a login completed by another process.
func (st *Store) Reload() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	b, err := os.ReadFile(st.path)
	if err != nil {
		return err
	}
	b, err = unprotect(b)
	if err != nil {
		return err
	}
	var next Session
	if err := json.Unmarshal(b, &next); err != nil {
		return err
	}
	if next.HostID != st.s.HostID || next.BridgeKey != st.s.BridgeKey {
		return errors.New("credential host or bridge key changed")
	}
	st.s = next
	return nil
}

func (st *Store) Update(fn func(*Session) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	next := st.s
	if err := fn(&next); err != nil {
		return err
	}
	if err := st.save(next); err != nil {
		return err
	}
	st.s = next
	return nil
}

func (st *Store) saveLocked() error {
	return st.save(st.s)
}

func (st *Store) save(s Session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	b, err = protect(b)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(st.path), ".chatgpt-plan-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), st.path)
}
