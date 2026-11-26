package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

// pair links two Conns back to back.
func pair() (*Conn, *Conn) {
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a, b := New(ar, aw), New(br, bw)
	go a.Serve()
	go b.Serve()
	return a, b
}

func TestCalls(t *testing.T) {
	a, b := pair()
	b.Handle("add", func(ctx context.Context, params json.RawMessage) (any, error) {
		var p struct{ X, Y int }
		_ = json.Unmarshal(params, &p)
		// a call back the other way while answering
		var echo string
		if err := b.Call(ctx, "echo", "hi", &echo); err != nil {
			return nil, err
		}
		return map[string]any{"sum": p.X + p.Y, "echo": echo}, nil
	})
	a.Handle("echo", func(ctx context.Context, params json.RawMessage) (any, error) {
		var s string
		_ = json.Unmarshal(params, &s)
		return s + "!", nil
	})
	b.Handle("fail", func(ctx context.Context, params json.RawMessage) (any, error) {
		return nil, &Error{Message: "nope", Code: "NOPE", Status: 409}
	})
	b.Handle("wait", func(ctx context.Context, params json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	got := make(chan string, 1)
	b.OnNotify("note", func(params json.RawMessage) { got <- string(params) })

	var out struct {
		Sum  int
		Echo string
	}
	if err := a.Call(context.Background(), "add", map[string]int{"X": 2, "Y": 3}, &out); err != nil || out.Sum != 5 || out.Echo != "hi!" {
		t.Fatal(out, err)
	}
	var re *Error
	if err := a.Call(context.Background(), "fail", nil, nil); !errors.As(err, &re) || re.Code != "NOPE" || re.Status != 409 {
		t.Fatal(err)
	}
	if err := a.Call(context.Background(), "missing", nil, nil); !errors.As(err, &re) || re.Code != "UNKNOWN_METHOD" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "wait", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	_ = a.Notify("note", map[string]int{"n": 1})
	if s := <-got; s != `{"n":1}` {
		t.Fatal(s)
	}
}
