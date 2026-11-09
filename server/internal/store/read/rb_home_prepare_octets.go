package read

// This file owns the octet discipline of the large Home preparation. Every new
// source stream reads only schema-enforced fixed-width catalogue columns
// (identities, kinds, revisions, nullability markers and 30-character
// timestamps), so no new variable octet point is introduced. The only variable
// values this reader hydrates are personal text and playable-binding fields,
// and those keep the fixed family's already registered exact same-snapshot
// byte points (rb.home.fixed.personal.bytes and the rb.home.fixed.binding.*
// ranges) through homeFixedBindingRows/homeFixedByteMatches. Nothing here is a
// shared dispatch list or a new registration.
//
// Each helper below proves the shape and the cap+1 witness of one window in the
// snapshot that produced it. A window longer than its declared cap is a source
// disagreement and refuses; it is never silently truncated.

// rbHomePrepareChronologyOctets validates one raw chronology window.
func rbHomePrepareChronologyOctets(rows [][]any, cap int) error {
	if cap < 1 || len(rows) > cap {
		return ErrUnqualified
	}
	for _, row := range rows {
		if len(row) != 8 {
			return ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return ErrUnqualified
		}
		if row[1] != nil {
			date, ok := row[1].(string)
			if !ok || len(date) != 30 {
				return ErrUnqualified
			}
		}
		public, ok := row[2].([]byte)
		if !ok || len(public) != 16 {
			return ErrUnqualified
		}
		if _, ok := row[3].(int64); !ok {
			return ErrUnqualified
		}
		if row[4] != nil {
			if _, ok := row[4].(int64); !ok {
				return ErrUnqualified
			}
		}
		for _, pos := range []int{5, 6, 7} {
			if row[pos] == nil {
				continue
			}
			if _, ok := row[pos].(int64); !ok {
				return ErrUnqualified
			}
		}
	}
	return nil
}

// rbHomePrepareChildOctets validates one complete bounded hierarchy child
// window.
func rbHomePrepareChildOctets(rows [][]any, cap int) error {
	if cap < 1 || len(rows) > cap {
		return ErrUnqualified
	}
	for _, row := range rows {
		if len(row) != 23 {
			return ErrUnqualified
		}
		entity, err := rbHomePrepareEntityFrom(row)
		if err != nil {
			return err
		}
		if entity.ID <= 0 {
			return ErrUnqualified
		}
	}
	return nil
}

// rbHomePreparePersonalOctets validates one complete current-personal window.
func rbHomePreparePersonalOctets(rows [][]any, cap int) error {
	if cap < 1 || len(rows) > cap {
		return ErrUnqualified
	}
	for _, row := range rows {
		if len(row) != 2 {
			return ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return ErrUnqualified
		}
		if row[1] == nil {
			continue
		}
		if _, ok := row[1].(int64); !ok {
			return ErrUnqualified
		}
	}
	return nil
}
