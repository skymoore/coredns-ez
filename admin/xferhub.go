package admin

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/coredns/coredns/plugin/transfer"
)

type xferBinding struct {
	xfer *transfer.Transfer
	core [][]string
}

// xferHub unions Corefile transfer { to } with extra IPs from sqlite and
// writes the result into every live transfer plugin (unexported xfr.to).
type xferHub struct {
	mu    sync.Mutex
	bound []*xferBinding
	extra []string
}

func newXferHub() *xferHub { return &xferHub{} }

func (h *xferHub) SetTransfer(x *transfer.Transfer) { h.AddTransfer(x) }

func (h *xferHub) AddTransfer(x *transfer.Transfer) {
	if h == nil || x == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, b := range h.bound {
		if b.xfer == x {
			h.publishLocked()
			return
		}
	}
	h.bound = append(h.bound, &xferBinding{xfer: x, core: captureXfrTo(x)})
	h.publishLocked()
}

func (h *xferHub) SetExtra(addrs []string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.extra = append([]string{}, addrs...)
	h.publishLocked()
}

func (h *xferHub) Corefile() []string {
	if h == nil {
		return []string{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	seen := map[string]struct{}{}
	for _, b := range h.bound {
		for _, row := range b.core {
			for _, a := range row {
				if _, ok := seen[a]; ok {
					continue
				}
				seen[a] = struct{}{}
				out = append(out, a)
			}
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (h *xferHub) publishLocked() {
	for _, b := range h.bound {
		applyXfrTo(b.xfer, b.core, h.extra)
	}
}

func captureXfrTo(x *transfer.Transfer) [][]string {
	if x == nil {
		return nil
	}
	v := reflect.ValueOf(x).Elem().FieldByName("xfrs")
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return nil
	}
	out := make([][]string, v.Len())
	for i := 0; i < v.Len(); i++ {
		item := v.Index(i)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				continue
			}
			item = item.Elem()
		}
		to := item.FieldByName("to")
		if !to.IsValid() || to.Kind() != reflect.Slice {
			continue
		}
		row := make([]string, to.Len())
		for j := 0; j < to.Len(); j++ {
			row[j] = to.Index(j).String()
		}
		out[i] = row
	}
	return out
}

func applyXfrTo(x *transfer.Transfer, core [][]string, extra []string) {
	v := reflect.ValueOf(x).Elem().FieldByName("xfrs")
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return
	}
	n := v.Len()
	if n > len(core) {
		n = len(core)
	}
	for i := 0; i < n; i++ {
		item := v.Index(i)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				continue
			}
			item = item.Elem()
		}
		merged := uniqueTransfer(append(append([]string{}, core[i]...), extra...))
		setUnexportedStringSlice(item, "to", merged)
	}
}

func uniqueTransfer(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, a := range in {
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

func setUnexportedStringSlice(structVal reflect.Value, field string, s []string) {
	f := structVal.FieldByName(field)
	if !f.IsValid() || f.Kind() != reflect.Slice {
		return
	}
	if s == nil {
		s = []string{}
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(s))
}
