package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"control/internal/db"
)

const (
	taskLLMPrices = "llm.prices"

	// The list ccusage prices from. Data, not code: a wrong entry misprices a
	// model, it cannot run anything here.
	llmPricesURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

	llmPricesEvery = 24 * time.Hour
	llmPricesRetry = time.Hour
)

type litellmEntry struct {
	Input      *float64 `json:"input_cost_per_token"`
	Output     *float64 `json:"output_cost_per_token"`
	CacheRead  *float64 `json:"cache_read_input_token_cost"`
	CacheWrite *float64 `json:"cache_creation_input_token_cost"`
}

func fetchLLMPrices(ctx context.Context) (db.UpsertLLMPricesParams, error) {
	var p db.UpsertLLMPricesParams
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, llmPricesURL, nil)
	if err != nil {
		return p, err
	}
	resp, err := llmHTTPClient.Do(req)
	if err != nil {
		return p, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("the price list returned %d", resp.StatusCode)
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&raw); err != nil {
		return p, fmt.Errorf("could not decode the price list: %w", err)
	}
	for model, blob := range raw {
		var e litellmEntry
		if json.Unmarshal(blob, &e) != nil || e.Input == nil || e.Output == nil {
			continue
		}
		// A model with no cache price is charged its input price for cached
		// tokens: an overestimate, never a free ride.
		p.Models = append(p.Models, model)
		p.Inputs = append(p.Inputs, *e.Input)
		p.Outputs = append(p.Outputs, *e.Output)
		p.CacheReads = append(p.CacheReads, orPrice(e.CacheRead, *e.Input))
		p.CacheWrites = append(p.CacheWrites, orPrice(e.CacheWrite, *e.Input))
	}
	if len(p.Models) == 0 {
		return p, fmt.Errorf("the price list had no usable entries")
	}
	return p, nil
}

func orPrice(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}

func handleLLMPrices(ctx context.Context, r *taskRunner, _ db.ScheduledTask) taskOutcome {
	p, err := fetchLLMPrices(ctx)
	if err != nil {
		return taskRetry(llmPricesRetry, "could not fetch llm prices: %v", err)
	}
	if err := r.q.UpsertLLMPrices(ctx, p); err != nil {
		return taskRetry(llmPricesRetry, "could not store llm prices: %v", err)
	}
	if _, err := scheduleTask(ctx, r.q, scheduleTaskParams{
		Kind:   taskLLMPrices,
		Reason: "refresh llm prices from litellm",
		After:  llmPricesEvery,
	}); err != nil {
		return taskRetry(llmPricesRetry, "stored %d llm prices but could not schedule the next refresh: %v", len(p.Models), err)
	}
	return taskDone("stored %d llm prices", len(p.Models))
}

type llmPrice struct {
	input, output, cacheRead, cacheWrite float64
}

// llmPriceBook reads the stored prices at most once a minute.
type llmPriceBook struct {
	q *db.Queries

	mu     sync.Mutex
	at     time.Time
	prices map[string]llmPrice
}

func (b *llmPriceBook) load(ctx context.Context) map[string]llmPrice {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.prices != nil && time.Since(b.at) < time.Minute {
		return b.prices
	}
	rows, err := b.q.ListLLMPrices(ctx)
	if err != nil {
		return b.prices
	}
	m := make(map[string]llmPrice, len(rows))
	for _, r := range rows {
		m[r.Model] = llmPrice{r.InputPerToken, r.OutputPerToken, r.CacheReadPerToken, r.CacheWritePerToken}
	}
	b.prices, b.at = m, time.Now()
	return m
}

// lookup tries the names litellm is known to file a model under: provider
// prefixed, bare, then under any other prefix.
func lookupLLMPrice(prices map[string]llmPrice, provider, model string) (llmPrice, bool) {
	model = strings.ToLower(model)
	for _, name := range []string{provider + "/" + model, model} {
		if p, ok := prices[name]; ok {
			return p, true
		}
	}
	// Map order is random; the smallest name keeps a price stable between reads.
	best := ""
	for name := range prices {
		if strings.HasSuffix(strings.ToLower(name), "/"+model) && (best == "" || name < best) {
			best = name
		}
	}
	if best == "" {
		return llmPrice{}, false
	}
	return prices[best], true
}
