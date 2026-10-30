import {useLayoutEffect, useRef, useState} from 'react';
import type {LibraryContentApi} from '@core/library-content.ts';
import {randomId} from '@core/random-id.ts';
import {LibraryLyricsFetch} from './lyrics-fetch';

/** One layout's exact command, its origin tuple and its synchronous in-flight
 * flag. A handler may only run the lease it was rendered with. */
class LyricsLease {
  readonly command: LibraryLyricsFetch;
  pending = false;
  constructor(readonly api: LibraryContentApi, readonly libraryId: string, readonly isCurrent: () => boolean) {
    this.command = new LibraryLyricsFetch({api, libraryId, isCurrent, randomId});
  }
  dispose() {this.command.dispose();}
}

/** One library's explicit lyrics command for the current layout. The command is
 * built in a layout effect, never during render, and replaced on every setup
 * replay; a saved callback therefore retires with its own lease instead of
 * adopting its successor. An uncertain result keeps its idempotency key in the
 * same command for a manual retry. */
export function useLibraryLyricsFetch({api, libraryId, isCurrent, onError}: Readonly<{
  api?: LibraryContentApi; libraryId: string; isCurrent: () => boolean; onError: (error: unknown) => void;
}>) {
  const [busy, setBusy] = useState(false);
  const live = useRef<LyricsLease | undefined>(undefined);
  const [lease, setLease] = useState<LyricsLease | undefined>(undefined);
  useLayoutEffect(() => {
    const next = api ? new LyricsLease(api, libraryId, isCurrent) : undefined;
    live.current = next;
    setLease(next);
    setBusy(false);
    return () => {
      // Retire synchronously: the lease stops being current before its command
      // is aborted, so no handler can observe a half-retired command.
      if (live.current === next) live.current = undefined;
      next?.dispose();
    };
  }, [api, libraryId, isCurrent]);
  const run = () => {
    const captured = lease;
    if (!captured || live.current !== captured) return;
    if (captured.api !== api || captured.libraryId !== libraryId || captured.isCurrent !== isCurrent) return;
    if (!isCurrent() || captured.pending) return;
    captured.pending = true;
    setBusy(true);
    void captured.command.run().catch(error => {
      if (live.current === captured && captured.isCurrent()) onError(error);
    }).finally(() => {
      if (live.current === captured) {captured.pending = false;if (captured.isCurrent()) setBusy(false);}
    });
  };
  return {busy, run};
}
