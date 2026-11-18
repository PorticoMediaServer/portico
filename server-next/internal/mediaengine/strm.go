package mediaengine

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/strm"
)

func (e *Engine) openSTRM(ctx context.Context, id int64, place db.FileOnDisk, remoteID int64) (*strm.File, error) {
	enabled, err := e.db.STRMEnabled(ctx, id)
	if err != nil || !enabled {
		return nil, strm.ErrUnavailable
	}
	var address string
	if remoteID != 0 {
		storage, _, err := e.remote(ctx, remoteID)
		if err != nil {
			return nil, strm.ErrUnavailable
		}
		pointer, err := storage.Open(ctx, place.Path)
		if err != nil {
			return nil, strm.ErrUnavailable
		}
		defer pointer.Close()
		if pointer.Size() != place.Size {
			return nil, strm.ErrUnavailable
		}
		address, err = strm.Pointer(pointer, pointer.Size())
		if err != nil {
			return nil, err
		}
	} else {
		file, err := libraryfile.Open(place.Path, e.folders())
		if err != nil {
			return nil, strm.ErrUnavailable
		}
		info, err := file.Stat()
		if err != nil || info.Size() != place.Size || info.ModTime().UnixNano() != place.ModifiedNS {
			file.Close()
			return nil, strm.ErrUnavailable
		}
		defer file.Close()
		address, err = strm.Pointer(file, info.Size())
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	client := e.strm
	if client == nil {
		client = strm.New()
	}
	return client.Open(ctx, address)
}

func (e *Engine) inspectSTRM(ctx context.Context, id int64, place db.FileOnDisk, remoteID int64) (mediafacts.Facts, error) {
	ctx, cancel := context.WithTimeout(ctx, strm.ReadTimeout)
	defer cancel()
	file, err := e.openSTRM(ctx, id, place, remoteID)
	if err != nil {
		problem := db.ProblemSTRMUnavailable
		switch {
		case errors.Is(err, strm.ErrRefused):
			problem = db.ProblemSTRMRefused
		case errors.Is(err, strm.ErrRedirects):
			problem = db.ProblemSTRMRedirects
		case errors.Is(err, strm.ErrRange):
			problem = db.ProblemSTRMRange
		case errors.Is(err, strm.ErrPointer):
			problem = db.ProblemSTRMPointer
		}
		_ = e.db.StoreProbes(ctx, []db.Probe{{FileID: id, Size: place.Size, ModifiedNS: place.ModifiedNS, Problem: problem}})
		return mediafacts.Facts{}, strm.ErrUnavailable
	}
	defer file.Close()
	file.LimitProbe()
	facts, err := inputbridge.Inspect(ctx, e.processes, e.probe.Binary, file, file.Size())
	if err != nil {
		_ = e.db.StoreProbes(ctx, []db.Probe{{FileID: id, Size: place.Size, ModifiedNS: place.ModifiedNS, Problem: db.ProblemNotMedia}})
		return mediafacts.Facts{}, strm.ErrMedia
	}
	facts.ObservedSize = file.Size()
	// Keep pointer identity/size separate from the facts of its target. Do not
	// cache target facts by pointer mtime: the outside media can change on its own.
	found := scan.FromMedia(facts)
	if err := e.db.StoreProbes(ctx, []db.Probe{{FileID: id, Size: place.Size, ModifiedNS: place.ModifiedNS,
		Container: found.Container, DurationMS: found.DurationMS, Bitrate: found.Bitrate, Streams: found.Streams, Chapters: found.Chapters}}); err != nil {
		return mediafacts.Facts{}, strm.ErrUnavailable
	}
	return facts, nil
}
