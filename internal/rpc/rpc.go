// Package rpc is the link between the JavaScript shim inside DSH and this
// process: newline-delimited JSON over stdin/stdout, requests in both directions.
//
//	request       {"id": 1, "method": "state", "params": {...}}
//	response      {"id": 1, "result": ...}  or  {"id": 1, "error": {"message": "...", "code": "...", "status": 409}}
//	notification  {"method": "guard.end", "params": {...}}
//	cancel        {"cancel": 1}   (the sender no longer waits for its request 1)
//
// Ids are per direction: a message with a method is a request (or a
// notification) of the other side, a message without one answers ours.
package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Error is a failure carried over the link.
type Error struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
	Status  int    `json:"status,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Handler answers one request; ctx ends when the caller cancels or the link closes.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// NotifyHandler takes one notification; notifications run one after another, in order.
type NotifyHandler func(params json.RawMessage)

type message struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
	Cancel *int64          `json:"cancel,omitempty"`
}

type response struct {
	ID     int64  `json:"id"`
	Result any    `json:"result"`
	Error  *Error `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    *Error
}

// ErrClosed is returned by calls on a closed link.
var ErrClosed = errors.New("rpc: the link to DSH is closed")

// Conn is one side of the link.
type Conn struct {
	r io.Reader

	wmu sync.Mutex
	w   io.Writer

	nextID atomic.Int64

	mu       sync.Mutex
	pending  map[int64]chan reply
	running  map[int64]context.CancelFunc
	handlers map[string]Handler
	notifies map[string]NotifyHandler
	closed   bool

	base   context.Context
	cancel context.CancelFunc
}

// New makes a link over a reader and a writer.
func New(r io.Reader, w io.Writer) *Conn {
	base, cancel := context.WithCancel(context.Background())
	return &Conn{
		r: r, w: w,
		pending:  map[int64]chan reply{},
		running:  map[int64]context.CancelFunc{},
		handlers: map[string]Handler{},
		notifies: map[string]NotifyHandler{},
		base:     base, cancel: cancel,
	}
}

// Handle registers the handler of a method.
func (c *Conn) Handle(method string, h Handler) {
	c.mu.Lock()
	c.handlers[method] = h
	c.mu.Unlock()
}

// OnNotify registers the handler of a notification.
func (c *Conn) OnNotify(method string, h NotifyHandler) {
	c.mu.Lock()
	c.notifies[method] = h
	c.mu.Unlock()
}

// Context ends when the link closes.
func (c *Conn) Context() context.Context { return c.base }

func (c *Conn) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(data)
	return err
}

// Call sends a request and decodes its result into out (which may be nil).
// When ctx ends first, the other side is told to cancel.
func (c *Conn) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan reply, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()
	raw, err := json.Marshal(params)
	if err != nil {
		c.drop(id)
		return err
	}
	if err := c.write(message{ID: &id, Method: method, Params: raw}); err != nil {
		c.drop(id)
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && len(r.result) > 0 {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-ctx.Done():
		c.drop(id)
		_ = c.write(message{Cancel: &id})
		return ctx.Err()
	case <-c.base.Done():
		return ErrClosed
	}
}

func (c *Conn) drop(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(message{Method: method, Params: raw})
}

// Serve reads the link until it closes; requests run concurrently.
func (c *Conn) Serve() error {
	reader := bufio.NewReaderSize(c.r, 1<<16)
	defer c.shutdown()
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (c *Conn) shutdown() {
	c.mu.Lock()
	c.closed = true
	for _, cancel := range c.running {
		cancel()
	}
	c.mu.Unlock()
	c.cancel()
}

func (c *Conn) dispatch(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	switch {
	case m.Cancel != nil:
		c.mu.Lock()
		cancel := c.running[*m.Cancel]
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case m.Method != "" && m.ID != nil:
		c.mu.Lock()
		h := c.handlers[m.Method]
		ctx, cancel := context.WithCancel(c.base)
		c.running[*m.ID] = cancel
		c.mu.Unlock()
		go c.run(*m.ID, m.Method, h, ctx, cancel, m.Params)
	case m.Method != "":
		c.mu.Lock()
		h := c.notifies[m.Method]
		c.mu.Unlock()
		if h != nil {
			h(m.Params) // in order: notifications must be quick
		}
	case m.ID != nil:
		c.mu.Lock()
		ch := c.pending[*m.ID]
		delete(c.pending, *m.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- reply{result: m.Result, err: m.Error}
		}
	}
}

func (c *Conn) run(id int64, method string, h Handler, ctx context.Context, cancel context.CancelFunc, params json.RawMessage) {
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.running, id)
		c.mu.Unlock()
	}()
	var result any
	var err error
	if h == nil {
		err = &Error{Message: fmt.Sprintf("unknown method %q", method), Code: "UNKNOWN_METHOD"}
	} else {
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("internal error in %s: %v", method, p)
				}
			}()
			result, err = h(ctx, params)
		}()
	}
	resp := response{ID: id, Result: result}
	if err != nil {
		var re *Error
		if errors.As(err, &re) {
			resp.Error = re
		} else {
			resp.Error = &Error{Message: err.Error()}
		}
		resp.Result = nil
	}
	_ = c.write(resp)
}
