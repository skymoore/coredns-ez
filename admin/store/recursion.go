package store

import "net"

func (s *Store) Recursion() []string {
	v, err := s.Meta(MetaRecursion)
	if err != nil || v == "" {
		return []string{}
	}
	out := SplitCSV(v)
	if out == nil {
		return []string{}
	}
	return out
}

func (s *Store) RecursionConfigured() bool {
	_, err := s.Meta(MetaRecursion)
	return err == nil
}

func (s *Store) SetRecursion(nets []string) error {
	out, err := NormalizeCIDRs(nets)
	if err != nil {
		return err
	}
	return s.SetMeta(MetaRecursion, JoinCSV(out))
}

func (s *Store) RecursionAllows(ip net.IP) bool {
	return ACL{Networks: s.Recursion()}.Contains(ip)
}

// EnsureRecursion writes the recursion allow-list if it has never been set.
// Existing ACL networks are copied once so a node that already recursed for
// split-horizon clients does not lock them out on upgrade.
func (s *Store) EnsureRecursion() error {
	if s.RecursionConfigured() {
		return nil
	}
	acls, err := s.ListACLs()
	if err != nil {
		return err
	}
	var nets []string
	for _, a := range acls {
		nets = append(nets, a.Networks...)
	}
	return s.SetRecursion(nets)
}
