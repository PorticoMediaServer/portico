package metadataprovider

import (
	"encoding/base64"
	"regexp"
	"strings"
)

var fingerprintSyntax = regexp.MustCompile(`^[A-Za-z0-9_-]+={0,2}$`)

// ValidFingerprint validates the bounded Chromaprint transport envelope, not
// acoustic quality. It does not generate fingerprints. Header and packed-delta
// layout: acoustid/chromaprint src/fingerprint_decompressor.cpp and chromaprint.h.
func ValidFingerprint(value string) bool {
	if len(value) < 8 || len(value) > 16384 || !fingerprintSyntax.MatchString(value) {
		return false
	}
	var data []byte
	var err error
	if strings.Contains(value, "=") {
		data, err = base64.URLEncoding.Strict().DecodeString(value)
	} else {
		data, err = base64.RawURLEncoding.Strict().DecodeString(value)
	}
	if err != nil || len(data) < 5 || data[0] > 4 {
		return false
	}
	count := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	if count < 1 || count > (len(data)-4)*8/3 {
		return false
	}
	body := data[4:]
	normal := make([]uint8, 0, min(len(body)*8/3, 32768))
	found, exceptional := 0, 0
	for bit := 0; bit+3 <= len(body)*8; bit += 3 {
		v := fingerprintBits(body, bit, 3)
		normal = append(normal, v)
		if v == 0 {
			found++
			if found == count {
				break
			}
		} else if v == 7 {
			exceptional++
		}
	}
	if found != count {
		return false
	}
	offset := (len(normal)*3 + 7) / 8
	if offset+(exceptional*5+7)/8 > len(body) {
		return false
	}
	// A compressed delta cannot address a bit outside its 32-bit signature.
	last, ex := 0, 0
	for _, v := range normal {
		if v == 0 {
			last = 0
			continue
		}
		delta := int(v)
		if v == 7 {
			delta += int(fingerprintBits(body[offset:], ex*5, 5))
			ex++
		}
		last += delta
		if last > 32 {
			return false
		}
	}
	return true
}
func fingerprintBits(data []byte, offset, width int) uint8 {
	var v uint8
	for i := 0; i < width; i++ {
		v |= ((data[(offset+i)/8] >> uint((offset+i)%8)) & 1) << uint(i)
	}
	return v
}

// FingerprintCompatibility is the integration seam for externally produced
// evidence. Never pass a private signature array through as a Chromaprint value.
// Source freshness, whole-file membership, duration and opt-in are independently
// required by metadata.readMusicInputs before Lookup can run.
func FingerprintCompatibility(algorithm, value string) string {
	if algorithm != "chromaprint" {
		return "unsupported_algorithm"
	}
	if !ValidFingerprint(value) {
		return "invalid_fingerprint"
	}
	return "ready"
}
