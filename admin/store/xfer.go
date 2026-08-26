package store

func (s *Store) TransferTo() []string {
	v, err := s.Meta(MetaTransferTo)
	if err != nil || v == "" {
		return []string{}
	}
	out := SplitCSV(v)
	if out == nil {
		return []string{}
	}
	return out
}

func (s *Store) SetTransferTo(addrs []string) error {
	if addrs == nil {
		addrs = []string{}
	}
	return s.SetMeta(MetaTransferTo, JoinCSV(addrs))
}

func (s *Store) AddTransferTo(addr string) (bool, error) {
	cur := s.TransferTo()
	for _, a := range cur {
		if a == addr {
			return false, nil
		}
	}
	return true, s.SetTransferTo(append(cur, addr))
}
