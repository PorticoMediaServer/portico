package catalog

// Pipe-delimited sets cannot distinguish a separator from a literal label.
// Keep the authoritative SQL path when such a spelling exists; never claim
// that an ambiguous bucket is an exact permission count.
func (s *Service) ambiguousRestrictionKeys() (bool, error) {
	var ambiguous bool
	err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_attribute_terms WHERE field_id IN(1,2) AND instr(value_key,'|')>0)`).Scan(&ambiguous)
	return ambiguous, err
}
