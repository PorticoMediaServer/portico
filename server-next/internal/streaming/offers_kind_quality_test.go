package streaming

func offeredIDs(rungs []QualityRung) []string {
	out := []string{}
	for _, r := range rungs {
		out = append(out, r.ID)
	}
	return out
}
