// Package retry — port of backend/app/utils/retry.py.
//
// Reintentos con backoff exponencial + jitter para llamadas a APIs externas.
package retry

import (
	"fmt"
	"log/slog"
	"math/rand"
	"time"
)

// Options mirrors the decorator parameters of retry_with_backoff().
type Options struct {
	MaxRetries    int           // default 3
	InitialDelay  time.Duration // default 1s
	MaxDelay      time.Duration // default 30s
	BackoffFactor float64       // default 2.0
	Jitter        bool          // default true
	// OnRetry ya recibe el error y el número de intento (0-based + 1 como Python)
	OnRetry func(err error, attempt int)
}

// WithBackoff ejecuta fn con reintentos. Nota: Python reintentaba CUALQUIER
// excepción; en Go se reintenta siempre que fn devuelva error, salvo que se
// use ShouldRetry para filtrar.
func WithBackoff(opts Options, fn func() error) error {
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	if opts.InitialDelay == 0 {
		opts.InitialDelay = time.Second
	}
	if opts.MaxDelay == 0 {
		opts.MaxDelay = 30 * time.Second
	}
	if opts.BackoffFactor == 0 {
		opts.BackoffFactor = 2.0
	}

	logger := slog.Default()
	delay := opts.InitialDelay
	var lastErr error

	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if attempt == opts.MaxRetries {
			logger.Error(fmt.Sprintf("función agotó %d reintentos: %v", opts.MaxRetries, err))
			return err
		}

		current := time.Duration(float64(delay))
		if current > opts.MaxDelay {
			current = opts.MaxDelay
		}
		fact := 1.0
		if opts.Jitter {
			fact = 0.5 + rand.Float64()
		}
		current = time.Duration(float64(current) * fact)

		logger.Warn(fmt.Sprintf("intento %d falló: %v; reintentando en %.1fs...", attempt+1, err, current.Seconds()))
		if opts.OnRetry != nil {
			opts.OnRetry(err, attempt+1)
		}

		time.Sleep(current)
		delay = time.Duration(float64(delay) * opts.BackoffFactor)
	}
	return lastErr
}

// BatchResult mirrors call_batch_with_retry outcomes.
type BatchResult[T any] struct {
	Results  []T
	Failures []Failure
}

// Failure holds the failing item index/error like the Python dict.
type Failure struct {
	Index int
	Item  any
	Error string
}

// Batch calls fn for each item, retrying each individually
// (equivalent of RetryableAPIClient.call_batch_with_retry). Devuelve tanto
// resultados tipados como fallos; aborta con error si !continueOnFailure.
func Batch[T any, I any](items []I, opts Options, fn func(I) (T, error), continueOnFailure bool) (BatchResult[T], error) {
	var (
		results  []T
		failures []Failure
	)
	logger := slog.Default()
	for idx, item := range items {
		out, err := BatchItem(item, opts, fn)
		if err != nil {
			logger.Error(fmt.Sprintf("procesando item %d falló: %v", idx+1, err))
			failures = append(failures, Failure{Index: idx, Item: item, Error: err.Error()})
			if !continueOnFailure {
				return BatchResult[T]{Results: results, Failures: failures}, err
			}
			continue
		}
		if out != nil {
			results = append(results, *out)
		}
	}
	return BatchResult[T]{Results: results, Failures: failures}, nil
}

// BatchItem ejecuta WithBackoff sobre un item y devuelve su resultado
// tipado (nil en error) para que el llamante lo colecione.
func BatchItem[T any, I any](item I, opts Options, fn func(I) (T, error)) (*T, error) {
	var result T
	err := WithBackoff(opts, func() error {
		v, e := fn(item)
		if e == nil {
			result = v
			return nil
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
