import {useEffect, useRef, useState} from 'react';
import type {ContentEntry, ContentProjection} from '@core/library-content.ts';
import type {DownloadOptionsView} from '@core/downloads.ts';
import {Button} from '../../../kit/controls';
import {Menu, MenuItem} from '../../../kit/overlays';
import {DownloadNotice} from '../DownloadNotice';
import {runScreenCommand} from '../context';
import {t} from '../text';
import type {AudiobookBindings} from './bindings';

/** Container ids never masquerade as a picker/download leaf. */
export function AudiobookMore({binding, page, entry}: {binding: AudiobookBindings; page: ContentProjection; entry: ContentEntry}) {
  const [open, setOpen] = useState(false), [download, setDownload] = useState<Readonly<{options: DownloadOptionsView; request: AbortController; busy: boolean}>>();
  const anchor = useRef<HTMLButtonElement>(null), active = useRef<AbortController | undefined>(undefined);
  const effects = binding.context.effects;
  const leaf = entry.kind === 'audiobook_file' && entry.available !== false && !entry.missing;
  const restart = entry === page.entity && page.scope.view === 'book' && page.listening?.actions.includes('play');
  const target = {libraryId: page.scope.libraryId, itemId: entry.id, kind: entry.kind, title: entry.title};
  const current = () => {try {binding.current(page);return entry === page.entity || binding.service.getSnapshot().appended.some(section => section.entries.includes(entry));} catch {return false;}};
  const close = () => {active.current?.abort(); active.current = undefined;setDownload(undefined);};
  useEffect(() => close, [binding, page, entry]);
  const run = (work: () => unknown) => runScreenCommand(binding.context, () => {if (!current()) throw new DOMException('Audiobook page changed', 'AbortError');return work();});
  const destination = (effect: typeof effects.addToPlaylist) => run(() => {if (leaf && effect && anchor.current) return effect(target, anchor.current);});
  const loadDownload = () => run(async () => {
    if (!leaf || !effects.download) return;
    close();const request = new AbortController();active.current = request;
    const valid = () => active.current === request && !request.signal.aborted && current();
    const options = await effects.download.options(target, request.signal, valid);
    if (valid() && options.policy.allowDownloads) setDownload({options, request, busy: false});
  });
  if (!restart && !(leaf && (effects.addToPlaylist || effects.addToCollection || effects.deleteTitle || effects.download))) return null;
  return <><Menu open={open} onOpenChange={setOpen} title={entry.title} trigger={<Button ref={anchor} variant="ghost" icon="i-more" iconOnly aria-label={t('card.more', {title: entry.title})}/>}>
    {restart && <MenuItem icon="i-retry" onSelect={() => run(() => binding.primary(page, true))}>{t('action.startOver')}</MenuItem>}
    {leaf && effects.download && <MenuItem icon="i-download" onSelect={loadDownload}>{t('next.detail.downloadAction')}</MenuItem>}
    {leaf && effects.addToPlaylist && <MenuItem icon="i-listadd" onSelect={() => destination(effects.addToPlaylist)}>{t('next.selection.addToPlaylist')}</MenuItem>}
    {leaf && effects.addToCollection && <MenuItem icon="i-layers" onSelect={() => destination(effects.addToCollection)}>{t('next.selection.addToCollection')}</MenuItem>}
    {leaf && effects.deleteTitle && <MenuItem icon="i-trash" onSelect={() => destination(effects.deleteTitle)}>{t('next.selection.deleteMenu')}</MenuItem>}
  </Menu>
    {download && <DownloadNotice open onOpenChange={value => {if (!value) close();}} title={entry.title} options={download.options} busy={download.busy} returnFocusRef={anchor}
      onChoose={quality => run(async () => {
        if (!effects.download || download.busy || active.current !== download.request) return;
        setDownload({...download, busy: true});
        try {await effects.download.save(target, quality, download.request.signal, () => current() && active.current === download.request);close();}
        catch (error) {if (current() && active.current === download.request) setDownload({...download, busy: false});throw error;}
      })}/>}
  </>;
}
