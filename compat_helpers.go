package claudeagentsdk

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func osRemove(path string) error         { return os.Remove(path) }
func osRemoveAll(path string) error      { return os.RemoveAll(path) }
func filepathDir(path string) string     { return filepath.Dir(path) }
func filepathJoin(elem ...string) string { return filepath.Join(elem...) }
func timeNowISO() string                 { return time.Now().UTC().Format(time.RFC3339Nano) }

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func appendJSONL(path string, entry map[string]any) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, mustJSON(entry))
	return err
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
