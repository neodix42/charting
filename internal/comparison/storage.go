package comparison

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type savedView struct {
	Version int  `json:"version"`
	View    View `json:"view"`
}

func (m *Manager) viewPath() string { return filepath.Join(m.opts.Profile, "comparison-view.json") }
func (m *Manager) readView() (View, error) {
	path := m.viewPath()
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultView(), nil
	}
	if err != nil {
		return View{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > 16384 {
		return View{}, errors.New("saved Comparison view must be a regular file within 16 KiB; original preserved")
	}
	f, err := os.Open(path)
	if err != nil {
		return View{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return View{}, errors.New("saved Comparison view changed while opening; original preserved")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil {
		return View{}, err
	}
	if len(data) > 16384 {
		return View{}, errors.New("saved Comparison view exceeds 16 KiB; original preserved")
	}
	var value savedView
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return View{}, fmt.Errorf("saved Comparison view is invalid; original preserved: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF || value.Version != 1 {
		return View{}, errors.New("saved Comparison view has an unsupported format; original preserved")
	}
	view, err := normalizeView(value.View)
	if err != nil {
		return View{}, fmt.Errorf("saved Comparison view is invalid; original preserved: %w", err)
	}
	return view, nil
}
func (m *Manager) View() (View, error) { m.mu.Lock(); defer m.mu.Unlock(); return m.readView() }
func (m *Manager) SaveView(value View) (View, error) {
	view, err := normalizeView(value)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return View{}, errors.New("comparison is closed")
	}
	// A valid new selection cannot silently destroy unrecognized saved bytes.
	if _, err = m.readView(); err != nil {
		return View{}, err
	}
	data, err := json.Marshal(savedView{Version: 1, View: view})
	if err != nil {
		return View{}, err
	}
	if err = os.MkdirAll(m.opts.Profile, 0700); err != nil {
		return View{}, err
	}
	f, err := os.CreateTemp(m.opts.Profile, ".comparison-save-*")
	if err != nil {
		return View{}, err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return View{}, err
	}
	if closeErr != nil {
		return View{}, closeErr
	}
	if err = os.Rename(f.Name(), m.viewPath()); err != nil {
		return View{}, err
	}
	if directory, e := os.Open(m.opts.Profile); e == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return view, nil
}
