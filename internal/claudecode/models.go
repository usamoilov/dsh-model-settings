package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"
)

// ModelPattern is a Claude Code model value: an alias (`opus`, `sonnet[1m]`) or a model id.
var ModelPattern = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9.\-\[\]]{1,79}$`)

// Model is one option of the settings page.
type Model struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Model       *string `json:"model"`
	Description string  `json:"description"`
}

// Models is Claude Code's list, or the fallback aliases with the reason.
type Models struct {
	Source      string  `json:"source"` // "claude-code" or "fallback"
	Reason      string  `json:"reason,omitempty"`
	Models      []Model `json:"models"`
	Recommended *Model  `json:"recommended"`
}

// FallbackModels are aliases Claude Code always understands; used only when the live list is unavailable.
func FallbackModels() []Model {
	return []Model{
		{ID: "opus", Name: "Opus"},
		{ID: "sonnet", Name: "Sonnet"},
		{ID: "haiku", Name: "Haiku"},
	}
}

func fallback(reason string) Models {
	return Models{Source: "fallback", Reason: reason, Models: FallbackModels()}
}

// ToOptions turns the `models` of the initialize response into page options;
// `default` is Claude Code's own pick.
func ToOptions(list any) ([]Model, *Model) {
	items, _ := list.([]any)
	out := []Model{}
	var recommended *Model
	for _, item := range items {
		m, _ := item.(map[string]any)
		id, _ := m["value"].(string)
		if id == "" || !ModelPattern.MatchString(id) {
			continue
		}
		entry := Model{ID: id, Name: id}
		if name, _ := m["displayName"].(string); name != "" {
			entry.Name = name
		}
		if resolved, ok := m["resolvedModel"].(string); ok {
			entry.Model = &resolved
		}
		entry.Description, _ = m["description"].(string)
		if id == "default" {
			e := entry
			recommended = &e
		} else {
			out = append(out, entry)
		}
	}
	return out, recommended
}

// Prober asks Claude Code for its model list.
type Prober func(ctx context.Context) (any, error)

// ProcessProber starts Claude Code with no tools and a prompt that is never
// sent, reads the model list from the initialize response and ends it: no
// request reaches the model, no usage is spent.
func ProcessProber(binary string) Prober {
	return func(ctx context.Context) (any, error) {
		if binary == "" {
			return nil, ErrMissing
		}
		p, err := Start(ctx, binary, ProbeArgs(), os.TempDir(), Env())
		if err != nil {
			return nil, err
		}
		defer p.Stop(time.Second)
		resp, err := p.Initialize(ctx)
		if err != nil {
			return nil, err
		}
		return resp["models"], nil
	}
}

// ModelCache caches Claude Code's list: an hour for a live list, a minute
// for a fallback; concurrent callers share one probe.
type ModelCache struct {
	probe   Prober
	missing bool
	TTL     time.Duration
	Timeout time.Duration

	mu      sync.Mutex
	at      time.Time
	value   *Models
	pending chan struct{}
}

// NewModelCache makes a cache over a prober; missing = there is no Claude Code at all.
func NewModelCache(probe Prober, missing bool) *ModelCache {
	return &ModelCache{probe: probe, missing: missing, TTL: time.Hour, Timeout: 20 * time.Second}
}

func (c *ModelCache) run(ctx context.Context) Models {
	if c.missing {
		return fallback("sdk-missing")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	raw, err := c.probe(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return fallback(fmt.Sprintf("Claude Code did not answer within %d s", int(c.Timeout.Seconds())))
		}
		return fallback(err.Error())
	}
	models, recommended := ToOptions(raw)
	if len(models) == 0 {
		return fallback("empty")
	}
	return Models{Source: "claude-code", Models: models, Recommended: recommended}
}

// List returns the cached list; refresh probes again.
func (c *ModelCache) List(ctx context.Context, refresh bool) Models {
	for {
		c.mu.Lock()
		if c.value != nil && !refresh {
			ttl := c.TTL
			if c.value.Source == "fallback" {
				ttl = time.Minute
			}
			if time.Since(c.at) < ttl {
				v := *c.value
				c.mu.Unlock()
				return v
			}
		}
		if wait := c.pending; wait != nil {
			c.mu.Unlock()
			<-wait
			c.mu.Lock()
			v := *c.value
			c.mu.Unlock()
			return v
		}
		wait := make(chan struct{})
		c.pending = wait
		c.mu.Unlock()

		v := c.run(context.WithoutCancel(ctx))
		c.mu.Lock()
		c.value, c.at, c.pending = &v, time.Now(), nil
		c.mu.Unlock()
		close(wait)
		return v
	}
}
