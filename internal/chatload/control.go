package chatload

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Init is the first line the orchestrator writes to the generator.
type Init struct {
	Invitations []Invitation    `json:"invitations"`
	Stand       json.RawMessage `json:"stand"`
}

// StdioControl talks to tests/load/chat_load.py over line-delimited JSON:
// the generator writes {"event":"mark","phase":...,"edge":"start"|"end"}
// and waits for {"snapshot":{...}} or {"error":"..."}.
type StdioControl struct {
	mu  sync.Mutex
	in  *bufio.Scanner
	out io.Writer
}

// NewStdioControl reads replies from in and writes marks to out.
func NewStdioControl(in io.Reader, out io.Writer) *StdioControl {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 16<<20)
	return &StdioControl{in: scanner, out: out}
}

// ReadInit reads the first line.
func (c *StdioControl) ReadInit() (Init, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var init Init
	if !c.in.Scan() {
		return init, fmt.Errorf("read init: %w", errOrEOF(c.in.Err()))
	}
	if err := json.Unmarshal(c.in.Bytes(), &init); err != nil {
		return init, fmt.Errorf("decode init: %w", err)
	}
	return init, nil
}

// Mark announces the edge of a phase and returns the orchestrator's snapshot.
func (c *StdioControl) Mark(phase, edge string) (*Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := MarshalLine(map[string]string{"event": "mark", "phase": phase, "edge": edge})
	if err != nil {
		return nil, err
	}
	if _, err := c.out.Write(line); err != nil {
		return nil, err
	}
	if !c.in.Scan() {
		return nil, fmt.Errorf("read snapshot: %w", errOrEOF(c.in.Err()))
	}
	var reply struct {
		Snapshot *Snapshot `json:"snapshot"`
		Error    string    `json:"error"`
	}
	if err := json.Unmarshal(c.in.Bytes(), &reply); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("orchestrator: %s", reply.Error)
	}
	return reply.Snapshot, nil
}

// Emit writes one event line, such as the final report.
func (c *StdioControl) Emit(event map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	line, err := MarshalLine(event)
	if err != nil {
		return err
	}
	_, err = c.out.Write(line)
	return err
}

func errOrEOF(err error) error {
	if err == nil {
		return io.ErrUnexpectedEOF
	}
	return errors.Join(io.ErrUnexpectedEOF, err)
}
