package storage

import "errors"

func replaceScanInput(argv []string, marker, path string) ([]string, error) {
	result := append([]string{}, argv...)
	count := 0
	for i := 1; i < len(result); i++ {
		if result[i] == marker || result[i] == "bluray:"+marker {
			prefix := ""
			if result[i] == "bluray:"+marker {
				prefix = "bluray:"
			}
			result[i] = prefix + path
			count++
		}
	}
	if count != 1 {
		return nil, errors.New("ambiguous retained scan input")
	}
	return result, nil
}
