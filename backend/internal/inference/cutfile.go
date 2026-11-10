package inference

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/trick77/almanach/internal/redact"
)

// cutKeep is how many files of a kind the folder keeps, the newest.
const cutKeep = 20

// The kinds of kept answers, each its own cutKeep: a run of unparseable
// answers never pushes out the runaway that was worth reading.
const (
	cutKindCut         = "cut"
	cutKindUnparseable = "unparseable"
)

// cutFile is one cut or unparseable call as keepCut writes it.
type cutFile struct {
	CallID    string    `json:"call_id"`
	Model     string    `json:"model"`
	At        time.Time `json:"at"`
	Err       string    `json:"err"`
	Answer    string    `json:"answer"`
	Reasoning string    `json:"reasoning"`
}

// unsafeName is what a call id may not carry into a file name.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// keepCut writes a cut or unparseable call whole to cutDir as
// <kind>-<call-id>.json and keeps the newest cutKeep of its kind: the
// gateway stores no answer ("Request/Response Data Not Available",
// 2026-11-10), so this is the only copy of what a runaway wrote. The parts
// come redacted, whole (logCut, unparseable). Its path, or "" for none: no
// cutDir, nothing but blanks, or a write that failed, which is logged and
// never fails the call.
func (c *Client) keepCut(kind, callID string, why error, parts ...cutPart) string {
	if c.cutDir == "" || !slices.ContainsFunc(parts, func(p cutPart) bool { return strings.TrimSpace(p.text) != "" }) {
		return ""
	}
	now := time.Now().UTC()
	f := cutFile{CallID: callID, Model: c.model, At: now}
	if why != nil {
		f.Err = redact.Text(why.Error())
	}
	for _, p := range parts {
		switch p.what {
		case "answer":
			f.Answer = p.text
		case "reasoning":
			f.Reasoning = p.text
		}
	}
	name := unsafeName.ReplaceAllString(callID, "_")
	if strings.Trim(name, "._") == "" {
		name = fmt.Sprintf("%d", now.UnixNano())
	}
	path := filepath.Join(c.cutDir, kind+"-"+name+".json")
	if err := writeCut(c.cutDir, path, f); err != nil {
		c.log.Warn("cut answer not kept", "call_id", callID, "path", path, "err", err.Error())
		return ""
	}
	pruneCuts(c.cutDir, kind+"-", cutKeep)
	return path
}

func writeCut(dir, path string, f cutFile) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// pruneCuts removes all but the newest keep files of a kind (prefix): a
// failed removal leaves one more, never an error.
func pruneCuts(dir, prefix string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		mod  time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{e.Name(), info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	slices.SortFunc(files, func(a, b file) int { return b.mod.Compare(a.mod) })
	for _, f := range files[keep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}
