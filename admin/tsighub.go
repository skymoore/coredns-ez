package admin

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"maps"
	"reflect"
	"sync"
	"unsafe"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin/transfer"
	"github.com/miekg/dns"
	"github.com/skymoore/coredns-ez/admin/store"
)

// tsigHub holds HMAC secrets for the live CoreDNS server. CoreDNS copies
// Config.TsigSecret at NewServer, so inbound verification uses miekg/dns
// TsigProvider (installed on the first query). Outgoing AXFR signing still
// reads transfer.Transfer.tsigSecret; we replace that map on publish.
type tsigHub struct {
	mu       sync.RWMutex
	keys     map[string]string
	corefile map[string]string
	xfer     *transfer.Transfer
}

func newTSIGHub() *tsigHub {
	return &tsigHub{keys: map[string]string{}, corefile: map[string]string{}}
}

func (h *tsigHub) MergeCorefile(secrets map[string]string) {
	if h == nil || len(secrets) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.corefile == nil {
		h.corefile = map[string]string{}
	}
	if h.keys == nil {
		h.keys = map[string]string{}
	}
	for k, v := range secrets {
		h.corefile[k] = v
		if _, ok := h.keys[k]; !ok {
			h.keys[k] = v
		}
	}
}

func (h *tsigHub) ReplaceDB(keys []store.TSIGKey) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	next := maps.Clone(h.corefile)
	if next == nil {
		next = map[string]string{}
	}
	for _, k := range keys {
		next[k.Name] = k.Secret
	}
	h.keys = next
	h.publishLocked()
}

func (h *tsigHub) Snapshot() map[string]string {
	if h == nil {
		return map[string]string{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return maps.Clone(h.keys)
}

func (h *tsigHub) SetTransfer(x *transfer.Transfer) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.xfer = x
	h.publishLocked()
}

func (h *tsigHub) publishLocked() {
	if h.xfer == nil {
		return
	}
	setUnexportedMap(h.xfer, "tsigSecret", maps.Clone(h.keys))
}

func (h *tsigHub) Generate(msg []byte, t *dns.TSIG) ([]byte, error) {
	h.mu.RLock()
	secret, ok := h.keys[dns.CanonicalName(t.Hdr.Name)]
	h.mu.RUnlock()
	if !ok {
		return nil, dns.ErrSecret
	}
	return tsigHMAC(secret, msg, t)
}

func (h *tsigHub) Verify(msg []byte, t *dns.TSIG) error {
	mac, err := h.Generate(msg, t)
	if err != nil {
		return err
	}
	want, err := hex.DecodeString(t.MAC)
	if err != nil {
		return err
	}
	if !hmac.Equal(mac, want) {
		return dns.ErrSig
	}
	return nil
}

func (h *tsigHub) Install(ctx context.Context) {
	if h == nil {
		return
	}
	v := ctx.Value(dnsserver.Key{})
	srv, ok := v.(*dnsserver.Server)
	if !ok || srv == nil {
		return
	}
	installTsigProvider(srv, h)
}

func tsigHMAC(secret string, msg []byte, t *dns.TSIG) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil {
		return nil, err
	}
	var hf func() hash.Hash
	switch dns.CanonicalName(t.Algorithm) {
	case dns.HmacSHA1:
		hf = sha1.New
	case dns.HmacSHA224:
		hf = sha256.New224
	case dns.HmacSHA256:
		hf = sha256.New
	case dns.HmacSHA384:
		hf = sha512.New384
	case dns.HmacSHA512:
		hf = sha512.New
	default:
		return nil, dns.ErrKeyAlg
	}
	mac := hmac.New(hf, raw)
	mac.Write(msg)
	return mac.Sum(nil), nil
}

func installTsigProvider(srv *dnsserver.Server, p dns.TsigProvider) {
	v := reflect.ValueOf(srv).Elem().FieldByName("server")
	if !v.IsValid() || v.Kind() != reflect.Array {
		return
	}
	for i := 0; i < v.Len(); i++ {
		slot := v.Index(i)
		if !slot.IsValid() || slot.IsNil() {
			continue
		}
		ds := (*dns.Server)(unsafe.Pointer(slot.UnsafePointer()))
		if ds.TsigProvider != p {
			ds.TsigProvider = p
		}
	}
}

func setUnexportedMap(ptr any, field string, m map[string]string) {
	if ptr == nil || m == nil {
		return
	}
	v := reflect.ValueOf(ptr)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	f := v.Elem().FieldByName(field)
	if !f.IsValid() || f.Kind() != reflect.Map {
		return
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(m))
}
