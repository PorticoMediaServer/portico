package writer

import "encoding/json"

func basicAnalysisHasOperation(raw, operation string) bool {
	var operations []string
	if json.Unmarshal([]byte(raw), &operations) != nil {
		return false
	}
	for _, op := range operations {
		if op == operation {
			return true
		}
	}
	return false
}
