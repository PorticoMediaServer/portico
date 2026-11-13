import type {RefObject} from 'react';
import type {ReadingClient} from '@portico/client-core';
import {defaultI18n} from '@i18n';
import {Button} from '../../kit/controls';
import {Dialog, MenuItem} from '../../kit/overlays';
import {useReadMark} from './ReadMarkAction';

const {t} = defaultI18n;
export type ReaderTitleVersion = Readonly<{sourceId: string; format: string; size: number}>;
/** The book behind an open reader, for its ⋯ More: the versions this reader may open (the one
 * open among them), the reading client for the read mark, and the way to the other version and
 * to the book's page. Navigation stays with the route that opened the reader. */
export type ReaderTitle = Readonly<{
  client: ReadingClient; itemId: string; sourceId: string; versions: readonly ReaderTitleVersion[];
  onVersion: (sourceId: string) => void; onDetails: () => void;
}>;

/** The book's mark, held while the reader is open so the menu opens with it already known. */
export function useReaderTitleMark(title: ReaderTitle | undefined, isCurrent: () => boolean, reportError: (error: unknown) => void) {
  return useReadMark({client: title?.client, itemId: title?.itemId ?? '', isCurrent, reportError});
}

/** The reader's ⋯ More for the book on every layout (desktop-read 6, phone-read 12): Read a
 * different version… when there is more than one, Mark as read or unread, and Book details. */
export function ReaderTitleItems({title, mark, onVersions, onClose}: Readonly<{title: ReaderTitle; mark: ReturnType<typeof useReaderTitleMark>; onVersions: () => void; onClose: () => void}>) {
  return <>
    {title.versions.length > 1 && <MenuItem icon="i-book" onSelect={() => {onClose(); onVersions();}}>{t('next.reader.readDifferentVersion')}</MenuItem>}
    {mark && <MenuItem icon="i-check" disabled={mark.busy} onSelect={mark.press}>{mark.label}</MenuItem>}
    <MenuItem icon="i-info" onSelect={() => {onClose(); title.onDetails();}}>{t('next.reader.bookDetails')}</MenuItem>
  </>;
}

/** The versions to read instead, the one open marked and not chosen again (as the book page's). */
export function ReaderVersionDialog({title, open, onOpenChange, returnFocusRef}: Readonly<{title: ReaderTitle; open: boolean; onOpenChange: (open: boolean) => void; returnFocusRef?: RefObject<HTMLElement | null>}>) {
  return <Dialog open={open} onOpenChange={onOpenChange} title={t('next.reader.readDifferentVersion')} returnFocusRef={returnFocusRef}>
    {title.versions.map(version => <Button key={version.sourceId} variant="glass" aria-pressed={version.sourceId === title.sourceId} disabled={version.sourceId === title.sourceId}
      onClick={() => {onOpenChange(false); title.onVersion(version.sourceId);}}>{version.format.toUpperCase()}{version.size > 0 && <span>{defaultI18n.bytes(version.size)}</span>}</Button>)}
  </Dialog>;
}
