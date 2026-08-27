package dnsupdatepersist

import (
	"fmt"
	"time"

	"github.com/miekg/dns"
)

// PersistFunc writes a committed zone to SQLite (or a test double).
type PersistFunc func(rrs []dns.RR) error

func (d *UpdatePersist) persistUpdated(rrs []dns.RR) error {
	if d.persistFn == nil {
		return fmt.Errorf("no persist backend")
	}
	start := time.Now()
	err := d.persistFn(rrs)
	writeDuration.WithLabelValues(d.Zone).Observe(time.Since(start).Seconds())
	if err == nil {
		log.Infof("Persisted %s serial=%d", d.Zone, soaOf(rrs).Serial)
	}
	return err
}
