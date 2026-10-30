package remotesources

import (
	"context"
	"errors"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type sourceRangeKey struct {
	observation               ObservationRef
	root, scanRevision, epoch string
	profile                   SourceReadProfile
	offset                    int64
	length                    int
}

type sourceRangeRead struct {
	done  chan struct{}
	users int
	data  []byte
	err   error
}

var errSharedSourceRangeInterrupted = errors.New("shared remote extent ended before completion")

// readSharedSourceRange shares only the same captured observation, profile,
// physical process and bounded extent. Each caller checks its own retained
// actor/target DB authority before joining and its own fresh source observation
// before any bytes are copied out. On cap1 this avoids a joiner's pre-read stat
// waiting behind the very transfer it can share. The admitted leader performs
// the common fresh start/reconnect checks against those exact scan facts.
func (s *Service) readSharedSourceRange(ctx context.Context, reader *SourceRange, output []byte, offset int64) (int, error) {
	if len(output) == 0 || len(output) > 1<<20 || offset < 0 || offset > reader.lease.binding.Size {
		return reader.session.ReadAt(ctx, output, offset)
	}
	if err := reader.lease.Check(ctx); err != nil {
		return 0, err
	}
	binding := reader.lease.binding
	reader.lease.mu.Lock()
	epoch := reader.lease.processGeneration
	reader.lease.mu.Unlock()
	if epoch == "" || !validSourceReadProfile(binding.Profile) {
		return 0, storage.ErrRemoteConfig
	}
	key := sourceRangeKey{binding.Observation, binding.Root, binding.ScanRevision, epoch, binding.Profile, offset, len(output)}
	s.sharedRangeMu.Lock()
	shared := s.sharedRanges[key]
	leader := shared == nil
	if leader {
		if s.sharedRangeCount[binding.Observation.SourceID] >= binding.Profile.Limits.Connections {
			s.sharedRangeMu.Unlock()
			return 0, storage.ErrBusy
		}
		if s.sharedRanges == nil {
			s.sharedRanges = make(map[sourceRangeKey]*sourceRangeRead)
			s.sharedRangeCount = make(map[string]int)
		}
		shared = &sourceRangeRead{done: make(chan struct{})}
		s.sharedRanges[key] = shared
		s.sharedRangeCount[binding.Observation.SourceID]++
	} else {
		s.sharedRangeJoiners.Add(1)
	}
	shared.users++
	s.sharedRangeMu.Unlock()
	defer func() {
		s.sharedRangeMu.Lock()
		shared.users--
		if shared.users == 0 {
			clear(shared.data)
			shared.data = nil
		}
		s.sharedRangeMu.Unlock()
	}()
	if leader {
		data := make([]byte, len(output))
		n, err := reader.session.ReadAt(ctx, data, offset)
		if err != nil && err != io.EOF {
			clear(data)
			data = nil
		} else {
			data = data[:n]
		}
		s.sharedRangeMu.Lock()
		shared.data, shared.err = data, err
		delete(s.sharedRanges, key)
		s.sharedRangeCount[binding.Observation.SourceID]--
		if s.sharedRangeCount[binding.Observation.SourceID] == 0 {
			delete(s.sharedRangeCount, binding.Observation.SourceID)
		}
		close(shared.done)
		s.sharedRangeMu.Unlock()
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-reader.session.life.Done():
		return 0, storage.ErrRemoteOffline
	case <-shared.done:
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if shared.err != nil && shared.err != io.EOF {
		if !leader {
			// A different reader's actor/request can end the shared transfer.
			// Its error never revokes this reader's still-current authority.
			// The child fails without retry; the caller rechecks its own lease.
			return 0, errSharedSourceRangeInterrupted
		}
		return 0, shared.err
	}
	// The leader's RangeSession.ReadAt already surrounded this transfer with
	// fresh observations. Recheck its current DB authority at this final copy
	// boundary; a follower must still obtain its own fresh session observation.
	var err error
	if leader {
		err = reader.lease.Check(ctx)
	} else {
		err = reader.session.Check(ctx)
	}
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.sharedRangeMu.Lock()
	n := copy(output, shared.data)
	s.sharedRangeMu.Unlock()
	return n, shared.err
}
