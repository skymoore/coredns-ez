package cachegen

import "sync/atomic"

// Epoch is folded into split-horizon-cache keys. Bump it after a zone or ACL
// mutation so a cached public wildcard cannot outlive the view record that
// should replace it. Entries from the previous epoch become unreachable and
// age out of the shards.
var epoch atomic.Uint64

func Bump() { epoch.Add(1) }

func Get() uint64 { return epoch.Load() }
