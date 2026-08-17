// The LLM wire: one chat-completions round trip against any
// OpenAI-compatible endpoint. The judge, the digester, and the
// expander all speak through this one type, so "the vera agent" is
// one model with one meter, playing three parts.
package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LLM is the vera agent's mouth: endpoint, credentials, model, meter.
// Base "" means the OpenAI API; any compatible server (ollama, LM
// Studio, a proxy) is a base URL away, and a non-default base needs no
// key.
type LLM struct {
	Client *http.Client
	Base   string
	Key    string
	Name   string // the model
	Effort string // reasoning_effort; "" omits the field
	Spend  func(cost float64)
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete is one round trip, metered: cost is priced from the usage
// the API reports where the price table knows the model — an unknown
// model completes fine and simply meters nothing, which is honest.
func (m *LLM) Complete(ctx context.Context, msgs []chatMsg) (string, error) {
	base := m.Base
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	body := struct {
		Model     string    `json:"model"`
		Messages  []chatMsg `json:"messages"`
		MaxTokens int       `json:"max_completion_tokens"`
		Effort    string    `json:"reasoning_effort,omitempty"`
	}{m.Name, msgs, 4096, m.Effort}
	buf, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.Key != "" {
		req.Header.Set("Authorization", "Bearer "+m.Key)
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		// The wire itself failed — nothing was judged. Transient.
		return "", MarkTransient(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", MarkTransient(err)
	}
	if resp.StatusCode != 200 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		err := fmt.Errorf("llm endpoint: HTTP %d", resp.StatusCode)
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			err = errors.New(e.Error.Message)
		}
		// Rate limits and server-side failures pass; a 4xx is our own
		// request being wrong and retrying it would be laps.
		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			return "", MarkTransient(err)
		}
		return "", err
	}
	var rep struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			In  int `json:"prompt_tokens"`
			Out int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return "", err
	}
	if len(rep.Choices) == 0 {
		return "", errors.New("llm endpoint: no choices")
	}
	if m.Spend != nil {
		if in, out, ok := price(m.Name); ok {
			m.Spend(in*float64(rep.Usage.In)/1e6 + out*float64(rep.Usage.Out)/1e6)
		}
	}
	return rep.Choices[0].Message.Content, nil
}

// price per million tokens (input, output), matched by longest prefix.
var prices = []struct {
	prefix  string
	in, out float64
}{
	{"gpt-5.6-luna", 0.20, 1.20},
	{"gpt-5-nano", 0.05, 0.40},
	{"gpt-5-mini", 0.25, 2.00},
	{"gpt-5", 1.25, 10.00},
	{"gpt-4o-mini", 0.15, 0.60},
	{"gpt-4o", 2.50, 10.00},
}

func price(model string) (in, out float64, ok bool) {
	best := -1
	for i, p := range prices {
		if strings.HasPrefix(model, p.prefix) && (best < 0 || len(p.prefix) > len(prices[best].prefix)) {
			best = i
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return prices[best].in, prices[best].out, true
}
