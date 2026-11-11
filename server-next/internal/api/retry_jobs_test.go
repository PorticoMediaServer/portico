package api

// Background jobs.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		var library string
		for _, lib := range th.libs {
			library = lib.PublicID
			break
		}
		return []retryCase{
			{route: "POST /v1/jobs", body: JobStart{Kind: "scan", Target: JobTarget{Library: library}},
				other: JobStart{Kind: "scan", Target: JobTarget{Library: library}, Params: JSON(`{"allFiles":true}`)}},
		}
	})
}
