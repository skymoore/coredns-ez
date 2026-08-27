package store

func (s *Store) ReplaceQueryBucket(b QueryBucket) error {
	if b.Types == "" {
		b.Types = "{}"
	}
	if b.Rcodes == "" {
		b.Rcodes = "{}"
	}
	if b.Names == "" {
		b.Names = "{}"
	}
	if b.Blocks == "" {
		b.Blocks = "{}"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Save(&b).Error
}

func (s *Store) ListQueryBuckets(from, to int64) ([]QueryBucket, error) {
	var rows []QueryBucket
	err := s.gdb.Where("ts >= ? AND ts <= ?", from, to).Order("ts").Find(&rows).Error
	return rows, err
}

func (s *Store) PruneQueryBuckets(before int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gdb.Where("ts < ?", before).Delete(&QueryBucket{}).Error
}
