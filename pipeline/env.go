package pipeline

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// ErrMemoryLimit reports that an operation would need more working memory than
// the configured limit allows (for example an extremely wide image whose
// minimum row buffers alone exceed the limit).
var ErrMemoryLimit = errors.New("goimgtools: memory limit exceeded")

// DefaultMemoryLimit is the per-pipeline working-memory budget used when the
// caller does not set one.
const DefaultMemoryLimit = 64 << 20

// Budget accounts for the large buffers a pipeline allocates. It is not a
// hard cap on the Go heap: small bookkeeping allocations are not tracked. It
// is what decides when data goes to disk instead of memory.
//
// A stage reserves before it allocates and releases when it frees:
//
//	need := int64(width) * 4 * int64(rows)
//	if err := env.Budget.Reserve(need, "my strip"); err != nil {
//		return nil, err // wraps ErrMemoryLimit
//	}
//	buf := make([]uint8, need)
//	...
//	env.Budget.Release(need) // in Close
//
// A Budget may be used from several goroutines.
type Budget struct {
	mu          sync.Mutex
	limit, used int64
	peak        int64
}

// NewBudget returns a budget of limit bytes; limit <= 0 means the default.
func NewBudget(limit int64) *Budget {
	if limit <= 0 {
		limit = DefaultMemoryLimit
	}
	return &Budget{limit: limit}
}

// Limit is the size of the budget in bytes.
func (b *Budget) Limit() int64 { return b.limit }

// Peak is the largest amount reserved at any one time.
func (b *Budget) Peak() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// Used is the amount currently reserved.
func (b *Budget) Used() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Reserve reserves n bytes for a buffer that is required to make progress.
func (b *Budget) Reserve(n int64, what string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || b.used+n > b.limit {
		return fmt.Errorf("%w: %s needs %d bytes, %d of %d already in use",
			ErrMemoryLimit, what, n, b.used, b.limit)
	}
	b.used += n
	b.peak = max(b.peak, b.used)
	return nil
}

// StagingShare is the part of the limit that data which could also be in a
// temporary file may take up in memory: one eighth.
//
// It is small on purpose. Staging on disk costs little (measured on 12 to
// 24 megapixel images: nothing for an interlaced GIF, 20% more time for a
// progressive JPEG, 0.1s for a rotation), while staging in memory up to the
// whole limit made middle-sized images peak at 20 to 45MB where larger
// ones, forced to disk, peak at 9MB. Images whose staged data is small
// (about 2 megapixels of RGBA at the default limit) never touch the disk.
const StagingShare = 8

// TryReserve reserves n bytes for data that may be staged in memory but
// does not have to be, only while everything reserved stays within
// 1/StagingShare of the limit; callers use a temporary file when it
// returns false.
func (b *Budget) TryReserve(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n < 0 || b.used+n > b.limit/StagingShare {
		return false
	}
	b.used += n
	b.peak = max(b.peak, b.used)
	return true
}

// Release returns n previously reserved bytes.
func (b *Budget) Release(n int64) {
	b.mu.Lock()
	b.used -= n
	b.mu.Unlock()
}

// Env is the shared context of one pipeline: its budget, temp directory
// and the helpers its stages may share their work with (SetHelpers).
type Env struct {
	Budget  *Budget
	TempDir string
	helpers chan struct{} // a token for each helper that is free
}

// NewEnv builds an Env; zero values mean defaults.
func NewEnv(limit int64, tempDir string) *Env {
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	return &Env{Budget: NewBudget(limit), TempDir: tempDir}
}
