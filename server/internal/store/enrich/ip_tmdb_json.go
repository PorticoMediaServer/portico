package enrich

// ValidTMDBObservationJSON applies the same strict syntax, UTF-8 and escaped
// surrogate checks to a bounded nonmovie observation as FIRST movie input.
// Callers still own their descriptor's typed shape/duplicate-key validation.
// This pure check neither copies the body nor grants decoding memory or proof.
func ValidTMDBObservationJSON(body []byte) bool { return validTMDBJSON(body) }
