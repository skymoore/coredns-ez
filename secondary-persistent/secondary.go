// Package secondarypersist implements the secondary-persistent plugin.
//
// Catalog member management is adapted from github.com/coredns/coredns/plugin/secondary
// (CoreDNS v1.14.7). Keep that behavior aligned when updating.
package secondarypersist

import (
	"sync"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/catalog"
)

const pluginName = "secondary-persistent"

// SecondaryPersist is a secondary DNS plugin that persists transferred zones to disk.
type SecondaryPersist struct {
	file.File

	zoneMu       sync.RWMutex
	zoneNames    map[*file.Zone]string
	dynamicZones map[string]*dynamicZone

	catalogMu          sync.RWMutex
	catalogs           map[string]*catalog.Catalog
	catalogZones       map[string]plugin.Zones
	catalogMemberZones map[string]map[string]struct{}

	records RecordStore

	persistMu       sync.Mutex
	lastSerial      map[string]uint32
	hasWritten      map[string]bool
	writing         map[string]bool
	pending         map[string]zoneSnapshot
	persistStop     chan struct{}
	persistStopOnce sync.Once
	persistWg       sync.WaitGroup
}

type dynamicZone struct {
	catalog  string
	memberID string
	shutdown chan bool
	stopOnce sync.Once
}

// Name implements the Handler interface.
func (s *SecondaryPersist) Name() string { return pluginName }
