package vortexruntime

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type auditSink struct {
	write func([]byte) (int, error)
	close func() error
}

func (s auditSink) Write(b []byte) (int, error) { return s.write(b) }
func (s auditSink) Close() error {
	if s.close != nil {
		return s.close()
	}
	return nil
}

func TestAuditIOFailures(t *testing.T) {
	sentinel := errors.New("injected")
	for _, stage := range []string{"open", "write", "short", "close", "write-and-close"} {
		t.Run(stage, func(t *testing.T) {
			d, _ := NewDeviceWithMode(Functional)
			d.auditPath = "injected"
			closed := false
			d.auditOpen = func(string) (io.WriteCloser, error) {
				if stage == "open" {
					return nil, sentinel
				}
				return auditSink{write: func(b []byte) (int, error) {
					if strings.Contains(stage, "write") {
						return 0, sentinel
					}
					if stage == "short" {
						return len(b) - 1, nil
					}
					return len(b), nil
				}, close: func() error {
					closed = true
					if strings.Contains(stage, "close") {
						return sentinel
					}
					return nil
				}}, nil
			}
			err := d.audit(auditRecord{Event: "test"})
			if err == nil || (stage != "short" && !errors.Is(err, sentinel)) || (stage == "short" && !errors.Is(err, io.ErrShortWrite)) {
				t.Fatalf("error=%v", err)
			}
			if stage != "open" && !closed {
				t.Fatal("writer not closed after failure")
			}
			launchOneLane(t, d, 0x100)
			if err := d.Start(); err == nil {
				t.Fatal("start ignored audit failure")
			}
			waitIdle(t, d)
			if !strings.Contains(d.LastError(), "op=audit") || d.LastRun().Retired != 0 {
				t.Fatal(d.LastRun(), d.LastError())
			}
		})
	}
}

// Stop inside Close, after the bytes have been written: Busy must remain true,
// and getters must remain callable without waiting for the audit lock.
func TestFinishAuditPrecedesIdle(t *testing.T) {
	for _, executionFails := range []bool{false, true} {
		for _, auditFails := range []bool{false, true} {
			t.Run(strings.Join([]string{boolName(executionFails), boolName(auditFails)}, "/"), func(t *testing.T) {
				d, _ := NewDeviceWithMode(Functional)
				d.auditPath = "injected"
				launchOneLane(t, d, 0x100)
				word := uint32(0x0000000b)
				if executionFails {
					word = 0xffffffff
				}
				putWord(t, d, 0x100, word)
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				defer once.Do(func() { close(release) })
				var terminal auditRecord
				d.auditOpen = func(string) (io.WriteCloser, error) {
					var event string
					return auditSink{write: func(b []byte) (int, error) {
						var r auditRecord
						if err := json.Unmarshal(b, &r); err != nil {
							return 0, err
						}
						event = r.Event
						if event == "launch-finish" {
							terminal = r
						}
						return len(b), nil
					}, close: func() error {
						if event == "launch-finish" {
							close(entered)
							<-release
							if auditFails {
								return errors.New("terminal-close-failed")
							}
						}
						return nil
					}}, nil
				}
				if err := d.Start(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-entered:
				case <-time.After(10 * time.Second):
					t.Fatal("missing terminal audit")
				}
				for i := 0; i < 50; i++ {
					if !d.Busy() {
						t.Fatal("idle published before audit close")
					}
					_ = d.LastRun()
					_ = d.LastError()
				}
				if (terminal.Summary.Error != "") != executionFails {
					t.Fatal(terminal)
				}
				once.Do(func() { close(release) })
				waitIdle(t, d)
				got := d.LastError()
				if (got != "") != (executionFails || auditFails) {
					t.Fatal(got)
				}
				if executionFails && (!strings.Contains(got, "simulator-internal") || d.LastRun().Origin != OriginSimulator) {
					t.Fatal("lost execution failure", got)
				}
				if auditFails && !strings.Contains(got, "terminal-close-failed") {
					t.Fatal("lost audit failure", got)
				}
			})
		}
	}
}
func boolName(v bool) string {
	if v {
		return "failure"
	}
	return "success"
}

func TestAuditRealFileAndDisabled(t *testing.T) {
	for _, mode := range []Mode{Functional, Timing} {
		t.Run(string(mode), func(t *testing.T) {
			d, _ := NewDeviceWithMode(mode)
			d.auditPath = filepath.Join(t.TempDir(), "events.jsonl")
			launchOneLane(t, d, 0x100)
			putWord(t, d, 0x100, 0x0000000b)
			if err := d.Start(); err != nil {
				t.Fatal(err)
			}
			waitIdle(t, d)
			if _, err := d.ReadDCR(dcrCacheFlush, 0); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(d.auditPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			count := 2
			if mode == Timing {
				count = 3
			}
			if len(lines) != count {
				t.Fatal(string(data))
			}
			for i, line := range lines {
				var r auditRecord
				if err := json.Unmarshal([]byte(line), &r); err != nil {
					t.Fatal(err)
				}
				if r.Summary.Sequence != 1 || r.Summary.Mode != mode {
					t.Fatal(r)
				}
				if i > 0 && (!strings.Contains(line, `"execution_cycles":`) || !strings.Contains(line, `"flush_cycles":`)) {
					t.Fatal("zero cycles omitted", line)
				}
			}
			d.auditPath = ""
			d.auditOpen = func(string) (io.WriteCloser, error) { t.Fatal("disabled audit opened"); return nil, nil }
			if err := d.audit(auditRecord{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTerminalAuditFailuresRemainObservable(t *testing.T) {
	for _, stage := range []string{"open", "write", "short", "close"} {
		for _, executionFails := range []bool{false, true} {
			t.Run(stage+"/"+boolName(executionFails), func(t *testing.T) {
				d, _ := NewDeviceWithMode(Functional)
				d.auditPath = "injected"
				launchOneLane(t, d, 0x100)
				word := uint32(0x0000000b)
				if executionFails {
					word = 0xffffffff
				}
				putWord(t, d, 0x100, word)
				opens := 0
				d.auditOpen = func(string) (io.WriteCloser, error) {
					opens++
					terminal := opens == 2
					if terminal && stage == "open" {
						return nil, errors.New("terminal-open")
					}
					return auditSink{write: func(b []byte) (int, error) {
						if terminal && stage == "write" {
							return 0, errors.New("terminal-write")
						}
						if terminal && stage == "short" {
							return 0, nil
						}
						return len(b), nil
					}, close: func() error {
						if terminal && stage == "close" {
							return errors.New("terminal-close")
						}
						return nil
					}}, nil
				}
				if err := d.Start(); err != nil {
					t.Fatal(err)
				}
				waitIdle(t, d)
				got := d.LastError()
				if !strings.Contains(got, "op=audit") {
					t.Fatal(got)
				}
				if executionFails && d.LastRun().Origin != OriginSimulator {
					t.Fatal("lost execution origin", got)
				}
			})
		}
	}
}

func TestFlushAuditPrecedesIdleAndReturnsFailure(t *testing.T) {
	d, _ := NewDeviceWithMode(Timing)
	d.auditPath = ""
	launchOneLane(t, d, 0x100)
	putWord(t, d, 0x100, 0x0000000b)
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, d)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	d.auditPath = "injected"
	d.auditOpen = func(string) (io.WriteCloser, error) {
		return auditSink{write: func(b []byte) (int, error) { return len(b), nil }, close: func() error {
			close(entered)
			<-release
			return errors.New("flush-close-failed")
		}}, nil
	}
	done := make(chan error, 1)
	go func() { _, err := d.ReadDCR(dcrCacheFlush, 0); done <- err }()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("missing flush audit")
	}
	if !d.Busy() || d.LastRun().BackingVisible {
		t.Fatal("flush published before audit")
	}
	once.Do(func() { close(release) })
	if err := <-done; err == nil || !strings.Contains(err.Error(), "flush-close-failed") {
		t.Fatal(err)
	}
	if d.Busy() || !strings.Contains(d.LastError(), "flush-close-failed") {
		t.Fatal(d.LastError())
	}
}
