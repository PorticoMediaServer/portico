package api

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// A move is short-lived, like the in-memory playback sessions it connects. A
// restart ends those sessions, so no persisted migration or transfer log is needed.
type castMove struct {
	source, item, target string
	until                time.Time
}
type castArea struct {
	castMoveMu sync.Mutex
	castMoves  map[string]castMove
}

func castReceiverKey(signIn int64) string { return "receiver:" + strconv.FormatInt(signIn, 10) }
func (s *Server) castRemember(key string, move castMove) {
	s.castMoveMu.Lock()
	defer s.castMoveMu.Unlock()
	if s.castMoves == nil {
		s.castMoves = map[string]castMove{}
	}
	for k, v := range s.castMoves {
		if !s.now().Before(v.until) {
			delete(s.castMoves, k)
		}
	}
	s.castMoves[key] = move
}
func (s *Server) castRedeemed(code string, signIn int64) {
	s.castMoveMu.Lock()
	defer s.castMoveMu.Unlock()
	if move, ok := s.castMoves[code]; ok && s.now().Before(move.until) {
		delete(s.castMoves, code)
		s.castMoves[castReceiverKey(signIn)] = move
	}
}
func (s *Server) castMoving(signIn int64, item string) string {
	s.castMoveMu.Lock()
	defer s.castMoveMu.Unlock()
	move := s.castMoves[castReceiverKey(signIn)]
	if move.item != item || !s.now().Before(move.until) {
		return ""
	}
	return move.source
}
func (s *Server) castStarted(signIn int64, item, target string) {
	s.castMoveMu.Lock()
	defer s.castMoveMu.Unlock()
	key := castReceiverKey(signIn)
	move, ok := s.castMoves[key]
	if ok && move.item == item && s.now().Before(move.until) && move.target == "" {
		move.target = target
		s.castMoves[key] = move
	}
}
func (s *Server) castPlaying(ctx context.Context, signIn, user int64, target string) {
	s.castMoveMu.Lock()
	key := castReceiverKey(signIn)
	move, ok := s.castMoves[key]
	if ok && move.target == target && s.now().Before(move.until) {
		delete(s.castMoves, key)
	} else {
		ok = false
	}
	s.castMoveMu.Unlock()
	if ok {
		s.playback.Stop(ctx, move.source, user, 0, nil)
	}
}
