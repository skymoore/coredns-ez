package admin

import (
	"net/http"
	"reflect"

	"github.com/coredns/coredns/core/dnsserver"
)

// installHTTPHandler sets Config.HTTPHandler when this binary was built with
// the coredns-http-handler patch. Reflection keeps the plugin compiling
// against unmodified CoreDNS v1.14.7; unpatched binaries simply 404 non-DoH
// paths on :443.
func installHTTPHandler(cfg *dnsserver.Config, h http.Handler) bool {
	v := reflect.ValueOf(cfg)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return false
	}
	f := v.Elem().FieldByName("HTTPHandler")
	if !f.IsValid() || !f.CanSet() {
		return false
	}
	f.Set(reflect.ValueOf(h))
	return true
}
