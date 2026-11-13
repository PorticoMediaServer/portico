package live

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"time"
)

// channelLogo brings a source's picture into the ordinary artwork store. Only
// its content hash leaves the server; an unavailable logo never loses a channel.
func (s *Service) channelLogo(ctx context.Context, address string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	const limit = 4 << 20
	body, err := s.fetch(ctx, address, limit+1)
	if err != nil {
		return "", err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	if len(data) > limit {
		return "", fmt.Errorf("the picture is too large")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	if err := s.pictures.Put(hash, data); err != nil {
		return "", err
	}
	return hash, nil
}
