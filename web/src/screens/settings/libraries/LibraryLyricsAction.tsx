import type {LibraryContentApi} from '@core/library-content.ts';
import {Button} from '../../../kit/controls';
import {useLibraryLyricsFetch} from './use-lyrics-fetch';

/** Receipt acknowledges a scheduled job only; it never represents completed lyrics. */
export function LibraryLyricsAction({api, libraryId, isCurrent, disabled, label, onError}: Readonly<{
  api: LibraryContentApi; libraryId: string; isCurrent: () => boolean; disabled: boolean; label: string; onError: (error: unknown) => void;
}>) {
  const {busy, run} = useLibraryLyricsFetch({api, libraryId, isCurrent, onError});
  return <Button variant="glass" size="small" disabled={disabled || busy} loading={busy} onClick={() => {void run();}}>{label}</Button>;
}
