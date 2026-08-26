package secondarypersist

import (
	"github.com/coredns/coredns/plugin/file"
	"github.com/coredns/coredns/plugin/pkg/catalog"
	"github.com/coredns/coredns/plugin/pkg/upstream"
)

type dynamicZoneStart struct {
	origin   string
	zone     *file.Zone
	shutdown chan bool
	hasData  bool
}

func (s *SecondaryPersist) applyCatalog(origin string, cat *catalog.Catalog, catalogZone *file.Zone) []dynamicZoneStart {
	memberZones := make(map[string]struct{}, len(cat.Members))
	var starts []dynamicZoneStart
	rejected := 0

	s.zoneMu.Lock()
	s.ensureZoneStateLocked()

	for _, member := range cat.Members {
		if !s.catalogMemberAllowed(origin, member.Zone) {
			rejected++
			continue
		}
		memberZones[member.Zone] = struct{}{}

		if existing, ok := s.Z[member.Zone]; ok {
			dyn, dynamic := s.dynamicZones[member.Zone]
			if !dynamic || existing == nil {
				log.Warningf("Skipping catalog member zone %s from %s: zone already exists", member.Zone, origin)
				continue
			}

			if dyn.catalog == origin {
				if dyn.memberID == member.ID {
					continue
				}
				previousID := dyn.memberID
				s.removePersistFile(member.Zone)
				start := s.replaceDynamicZoneLocked(member, origin, catalogZone, nil)
				starts = append(starts, start)
				log.Infof("Reset catalog member zone %s from %s after member ID changed from %s to %s", member.Zone, origin, previousID, member.ID)
				continue
			}

			s.catalogMu.RLock()
			sourceMember, ok := catalogMember(s.catalogs[dyn.catalog], member.Zone)
			if !ok || sourceMember.ChangeOfOwnership != origin {
				s.catalogMu.RUnlock()
				log.Warningf("Skipping catalog member zone %s from %s: zone already exists", member.Zone, origin)
				continue
			}
			start, preserved := s.migrateCatalogMemberLocked(existing, dyn, sourceMember, member, origin, catalogZone)
			s.catalogMu.RUnlock()
			starts = append(starts, start)
			s.logCatalogMigration(member.Zone, dyn.catalog, origin, sourceMember.ID, member.ID, preserved)
			continue
		}

		start := s.replaceDynamicZoneLocked(member, origin, catalogZone, nil)
		starts = append(starts, start)
		log.Infof("Added catalog member zone %s from catalog %s", member.Zone, origin)
	}

	for member := range s.catalogMemberZones[origin] {
		if _, ok := memberZones[member]; ok {
			continue
		}
		if s.removeDynamicZoneLocked(member, origin) {
			s.removePersistFile(member)
			log.Infof("Removed catalog member zone %s from catalog %s", member, origin)
		}
	}
	s.catalogMemberZones[origin] = memberZones
	s.zoneMu.Unlock()

	if rejected > 0 {
		log.Warningf("Skipped %d member zones from catalog %s: outside configured member zones", rejected, origin)
	}
	return starts
}

func (s *SecondaryPersist) catalogMemberAllowed(origin, member string) bool {
	zones, ok := s.catalogZones[origin]
	return ok && (len(zones) == 0 || zones.Matches(member) != "")
}

func catalogMember(cat *catalog.Catalog, zone string) (catalog.Member, bool) {
	if cat == nil {
		return catalog.Member{}, false
	}
	for _, member := range cat.Members {
		if member.Zone == zone {
			return member, true
		}
	}
	return catalog.Member{}, false
}

// migrateCatalogMemberLocked moves ownership to target. State is retained only
// when the active, source, and target member IDs all describe the same member.
func (s *SecondaryPersist) migrateCatalogMemberLocked(existing *file.Zone, dyn *dynamicZone, source, target catalog.Member, targetOrigin string, targetZone *file.Zone) (dynamicZoneStart, bool) {
	preserved := dyn.memberID == source.ID && source.ID == target.ID
	var preserveFrom *file.Zone
	if preserved {
		preserveFrom = existing
	} else {
		s.removePersistFile(target.Zone)
	}
	return s.replaceDynamicZoneLocked(target, targetOrigin, targetZone, preserveFrom), preserved
}

// replaceDynamicZoneLocked atomically installs a new transfer generation. A
// stopped generation may still finish an in-flight transfer, but it can only
// update the detached Zone pointer and cannot overwrite the new owner.
func (s *SecondaryPersist) replaceDynamicZoneLocked(member catalog.Member, origin string, catalogZone, preserveFrom *file.Zone) dynamicZoneStart {
	if dyn, ok := s.dynamicZones[member.Zone]; ok {
		dyn.stopOnce.Do(func() { close(dyn.shutdown) })
	}

	z := file.NewZone(member.Zone, "stdin")
	if catalogZone != nil {
		z.TransferFrom = append([]string(nil), catalogZone.TransferFrom...)
	}
	hasData := false
	if preserveFrom != nil {
		preserveDynamicZoneState(z, preserveFrom)
		hasData = zoneSOA(z) != nil
	}
	z.Upstream = upstream.New()

	if previous, ok := s.Z[member.Zone]; ok {
		if previous != nil {
			delete(s.zoneNames, previous)
		}
	} else {
		s.Names = append(s.Names, member.Zone)
	}
	shutdown := make(chan bool)
	s.Z[member.Zone] = z
	s.zoneNames[z] = member.Zone
	s.dynamicZones[member.Zone] = &dynamicZone{catalog: origin, memberID: member.ID, shutdown: shutdown}
	return dynamicZoneStart{origin: member.Zone, zone: z, shutdown: shutdown, hasData: hasData}
}

func preserveDynamicZoneState(target, source *file.Zone) {
	// Dynamic transfers replace the live Apex and Tree instead of mutating them,
	// so this snapshot remains isolated from any old in-flight transfer.
	source.RLock()
	target.Apex = source.Apex
	target.Tree = source.Tree
	target.Expired = source.Expired
	source.RUnlock()
}

func (s *SecondaryPersist) logCatalogMigration(zone, source, target, sourceID, targetID string, preserved bool) {
	if preserved {
		log.Infof("Migrated catalog member zone %s from catalog %s to %s, preserving state for member ID %s", zone, source, target, sourceID)
		return
	}
	log.Infof("Migrated catalog member zone %s from catalog %s to %s with state reset after member identity changed from %s to %s", zone, source, target, sourceID, targetID)
}

// removeDynamicZoneLocked removes a zone only when it belongs to catalog.
// The caller must hold s.zoneMu for writing.
func (s *SecondaryPersist) removeDynamicZoneLocked(zone, catalog string) bool {
	dyn, ok := s.dynamicZones[zone]
	if !ok || dyn.catalog != catalog {
		return false
	}
	dyn.stopOnce.Do(func() { close(dyn.shutdown) })
	delete(s.dynamicZones, zone)
	if z := s.Z[zone]; z != nil {
		delete(s.zoneNames, z)
	}
	delete(s.Z, zone)
	s.Names = removeZoneName(s.Names, zone)
	return true
}

func (s *SecondaryPersist) ensureZoneStateLocked() {
	if s.Z == nil {
		s.Z = make(map[string]*file.Zone)
	}
	if s.zoneNames == nil {
		s.zoneNames = make(map[*file.Zone]string, len(s.Z))
		for name, zone := range s.Z {
			if zone != nil {
				s.zoneNames[zone] = name
			}
		}
	}
	if s.dynamicZones == nil {
		s.dynamicZones = make(map[string]*dynamicZone)
	}
	if s.catalogMemberZones == nil {
		s.catalogMemberZones = make(map[string]map[string]struct{})
	}
}
