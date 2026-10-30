package metadataprovider

// BoundScreenRecord rejects the smallest invalid optional unit. The record
// validator still enforces every count, byte, identifier and character bound.
func BoundScreenRecord(r ScreenRecord) ScreenRecord {
	before := len(r.Aliases) + len(r.Genres) + len(r.Tags) + len(r.Credits) + len(r.Relations) + len(r.Crosswalk) + len(r.Orders) + len(r.Certifications)
	r.Aliases = boundedScreenList(r.Aliases, 256, func(s string) bool { return cleanScreenText(s, 2048) })
	r.Genres = boundedScreenList(r.Genres, 64, func(n ScreenName) bool { return cleanScreenText(n.Name, 200) && len(n.ID) <= 128 })
	r.Tags = boundedScreenList(r.Tags, 64, func(n ScreenName) bool { return cleanScreenText(n.Name, 200) && len(n.ID) <= 128 })
	r.Credits = boundedScreenList(r.Credits, 128, func(c ScreenCredit) bool {
		return len(c.ID) <= 128 && cleanScreenText(c.Name, 300) && cleanScreenText(c.Role, 500) && cleanScreenText(c.Department, 200) && c.Ordinal >= 0
	})
	r.Relations = boundedScreenList(r.Relations, 256, func(v ScreenRelation) bool {
		return ValidScreenID(v.Target) && len(v.Kind) <= 100 && cleanScreenText(v.Name, 2048)
	})
	r.Crosswalk = boundedScreenList(r.Crosswalk, 16, ValidScreenID)
	r.Orders = boundedScreenList(r.Orders, 64, func(v ScreenOrder) bool { return len(v.ID) <= 160 && cleanScreenText(v.Name, 2048) })
	r.Certifications = boundedScreenList(r.Certifications, 100, func(v ScreenCertification) bool {
		return cleanScreenText(v.Region, 64) && cleanScreenText(v.Rating, 128)
	})
	after := len(r.Aliases) + len(r.Genres) + len(r.Tags) + len(r.Credits) + len(r.Relations) + len(r.Crosswalk) + len(r.Orders) + len(r.Certifications)
	if after < before {
		found := false
		for _, issue := range r.Skipped {
			found = found || issue == "provider_fields_skipped"
		}
		if !found && len(r.Skipped) < 16 {
			r.Skipped = append(r.Skipped, "provider_fields_skipped")
		}
	}
	return r
}
func boundedScreenList[T any](list []T, limit int, valid func(T) bool) []T {
	out := make([]T, 0, min(len(list), limit))
	for _, v := range list {
		if len(out) == limit {
			break
		}
		if valid(v) {
			out = append(out, v)
		}
	}
	return out
}
