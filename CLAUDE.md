# devbrain — agent notes

## Concurrency
- Signal last: finish ALL shared-state cleanup (map deletes, field writes) BEFORE
  signaling waiters (`close(ch)`, `wg.Done`, cond broadcast). A waiter that wakes
  early must never observe the finished work still registered.
- A change touching `sync.`, `chan`, or `go func` must pass
  `go test -race -count=10` on its package before the PR opens — one run of a
  timing-window test is a coin flip; ten is a test.
