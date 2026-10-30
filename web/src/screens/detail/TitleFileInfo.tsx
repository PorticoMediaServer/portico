import {useCallback, useEffect, useMemo, useState, useSyncExternalStore, type RefObject} from 'react';
import type {DetailProjection, FileInfoDisc} from '@core/detail.ts';
import {fileInfoTrackKey, fileInfoUsesGroups, type FileInfoLane, type FileInfoSession, type FileInfoTrackLane} from '@core/file-info-session.ts';
import type {FileInfoFileRow, FileInfoGroupRow, FileInfoSurface} from '@core/file-info-pages.ts';
import {fileInfoFileFacts, fileInfoPanelModel, fileInfoVersionFacts} from '@core/presentation/file-info-versions.ts';
import type {FileFacts} from '@core/presentation/kit-model';
import {Dialog, DiscTitles, FileInfo, type FileInfoAction, type FileInfoContinuation, type FileInfoMore} from '../../kit/overlays';
import {StateView} from '../../kit/states';
import type {DetailBindings} from './bindings';
import {ScreenFailure} from './Detail';
import {DetailLoading} from './Loading';
import {t, environment} from './text';

/** What an owner's Delete, Split or Merge was chosen from: the opening, and the versions and
 * files that were on screen. Commands send the exact occurrence; the server rechecks the rest. */
export type FileInfoManageContext = Readonly<{
  action: FileInfoAction; opening: string; surface: FileInfoSurface;
  /** The files that were listed, by exact occurrence. */
  files: ReadonlyMap<string, FileInfoFileRow>;
  /** What Split can make a title of: a version that is one file on its own, or any flat file. */
  splitCandidates: readonly Readonly<{id: string; occurrence: string; facts: FileFacts}>[];
  /** The title's one file, when the whole list is known to be exactly one: the exact source a Merge
   * of this title away proves. Absent when there are more, or more could follow. */
  sourceOccurrence?: string;
}>;
type Props = {
  binding: DetailBindings; data: DetailProjection; surface: 'file' | 'titles'; phone: boolean;
  returnFocusRef: RefObject<HTMLButtonElement | null>; onClose: () => void; onManage: (context: FileInfoManageContext) => void;
};

/** File info and the disc's Titles for one opening of one accepted Detail. Versions, their
 * tracks and the owner's files are read window by window as they are shown; the session and
 * every lease it holds go with the panel. */
export function TitleFileInfo({binding, data, surface, phone, returnFocusRef, onClose, onManage}: Props) {
  const [session, setSession] = useState<FileInfoSession | null>();
  useEffect(() => {
    const opened = binding.openFileInfo(data);
    setSession(opened ?? null);
    void (fileInfoUsesGroups(data.item.kind) ? opened?.groups() : opened?.files());
    return () => opened?.retire();
  }, [binding, data]);
  // A Detail that is not ready yet has no opening; reading it again brings one.
  if (session === null) return <Dialog open onOpenChange={open => { if (!open) onClose(); }} title={t(surface === 'titles' ? 'next.overlays.discTitles' : 'next.overlays.fileInfo')} returnFocusRef={returnFocusRef}>
    <ScreenFailure retry={() => { void binding.service.revalidate(); }} />
  </Dialog>;
  return session ? <Opened session={session} binding={binding} data={data} surface={surface} phone={phone} returnFocusRef={returnFocusRef} onClose={onClose} onManage={onManage} /> : null;
}

/** One listed entry: a version of a playable item, or a file of any other kind. */
type Entry = Readonly<{id: string; head: string; audioCount: number; subtitleCount: number; group?: FileInfoGroupRow; file?: FileInfoFileRow}>;

function Opened({session, binding, data, surface, phone, returnFocusRef, onClose, onManage}: Props & {session: FileInfoSession}) {
  const snapshot = useSyncExternalStore(session.subscribe, session.getSnapshot, session.getSnapshot);
  const flat = !fileInfoUsesGroups(data.item.kind), manage = snapshot.surface?.manage;
  const lane: FileInfoLane<unknown> | undefined = flat ? snapshot.files : snapshot.groups;
  const read = useCallback((mode: 'open' | 'next' | 'restart') => { void (flat ? session.files(mode) : session.groups(mode)); }, [flat, session]);
  const entries = useMemo((): readonly Entry[] | undefined => flat
    ? snapshot.files?.rows?.rows.map(file => ({id: file.occurrence, head: file.occurrence, audioCount: file.audioCount, subtitleCount: file.subtitleCount, file}))
    : snapshot.groups.rows?.rows.map(group => ({id: group.id, head: group.headOccurrence, audioCount: group.audioCount, subtitleCount: group.subtitleCount, group})), [flat, snapshot.files, snapshot.groups]);
  const close = (open: boolean) => { if (!open) onClose(); };
  // The owner's disc block shows in File info and in Titles; members never have one.
  useEffect(() => {
    if (snapshot.surface?.ownerDiscOccurrence && !snapshot.disc) void session.disc();
  }, [session, snapshot.surface, snapshot.disc]);

  const more = <Row,>(held: FileInfoLane<Row> | undefined, next: () => void): FileInfoMore | undefined =>
    held && (held.rows?.more || held.status === 'failed') ? {busy: held.status === 'loading' || held.status === 'preparing', failed: held.status === 'failed', onPress: next} : undefined;
  const byId = useMemo(() => new Map(entries?.map(entry => [entry.id, entry])), [entries]);
  const continuation = useMemo((): FileInfoContinuation | undefined => entries && {
    versionCount: (flat ? snapshot.surface?.fileCount : snapshot.surface?.groupCount) ?? entries.length,
    more: more(lane, () => read(lane?.status === 'failed' && !lane.rows?.more ? 'restart' : 'next')),
    earlier: lane?.rows?.earlier ? {busy: false, failed: false, onPress: () => read('restart')} : undefined,
    onVersionVisible: id => {
      const entry = byId.get(id);
      if (!entry) return;
      if (entry.audioCount > 0) void session.tracks(entry.head, 'audio');
      if (entry.subtitleCount > 0) void session.tracks(entry.head, 'subtitles');
      if (manage && entry.group) void session.members(entry.group.id);
    },
    tracks: (id, kind: FileInfoTrackLane) => {
      const entry = byId.get(id);
      if (!entry || (kind === 'audio' ? entry.audioCount : entry.subtitleCount) === 0) return undefined;
      const held = snapshot.tracks.get(fileInfoTrackKey(entry.head, kind));
      return {pending: !held?.rows, more: more(held, () => { void session.tracks(entry.head, kind, held?.rows?.more ? 'next' : 'open'); })};
    },
    locationsMore: id => flat ? undefined : more(snapshot.members.get(id), () => { void session.members(id, snapshot.members.get(id)?.rows?.more ? 'next' : 'open'); }),
  }, [entries, byId, flat, lane, manage, read, session, snapshot]);

  // Web lists a version head's raw tracks; Merge is drawn in the phone sheet only (phone-detail 12).
  const model = useMemo(() => fileInfoPanelModel(snapshot, {tracks: 'raw', context: phone ? 'phone' : 'desktop', manageSurface: phone ? 'phone' : 'desktop', split: true, merge: phone}, environment), [phone, snapshot]);

  // The disc block names the entry whose file it was read from.
  const disc = useMemo((): FileInfoDisc | undefined => {
    const held = snapshot.disc, titles = held?.rows?.rows;
    if (!held?.occurrence || !held.discKind || !titles?.length) return undefined;
    const entry = entries?.find(value => value.head === held.occurrence);
    return {fileId: entry?.id ?? held.occurrence, kind: held.discKind, titles};
  }, [snapshot.disc, entries]);

  const failed = lane?.status === 'failed' && !entries;
  // A stale opening (409) is refreshed through the Detail: the newer Detail is a new opening.
  const stale = (error: unknown) => (error as {code?: string} | undefined)?.code === 'stale_continuation';
  const retry = () => { if (stale(lane?.error)) void binding.service.revalidate(); else read('restart'); };
  if (surface === 'titles') {
    const owner = snapshot.surface && !!snapshot.surface.ownerDiscOccurrence;
    if (disc) return <DiscTitles open onOpenChange={close} disc={disc} file={model?.files.find(file => file.id === disc.fileId)?.display.heading.text} more={more(snapshot.disc, () => { void session.disc('next'); })} returnFocusRef={returnFocusRef} />;
    return <Dialog open onOpenChange={close} title={t('next.overlays.discTitles')} returnFocusRef={returnFocusRef}>
      {failed || snapshot.disc?.status === 'failed' ? <ScreenFailure retry={failed ? retry : () => { void session.disc('restart'); }} />
        : snapshot.surface && !owner ? <StateView title={t('title.nothingHere')} /> : <DetailLoading shape="panel" />}
    </Dialog>;
  }
  if (!model && failed) return <Dialog open onOpenChange={close} title={t('next.overlays.fileInfo')} returnFocusRef={returnFocusRef}><ScreenFailure retry={retry} /></Dialog>;
  // Preparing (including the opening preparation, which may not name its Detail yet): the panel
  // itself, with quiet version-shaped lines, so the versions take their place without a jump.
  if (!model) return <FileInfo pending model={{heading: t('next.kit.fileInfo'), files: []}} subject={data.item.title} layout={phone ? 'phone' : 'desktop'} open onOpenChange={close} returnFocusRef={returnFocusRef} onManage={() => {}} />;
  return <FileInfo model={model} subject={data.item.title} disc={disc} continuation={continuation} layout={phone ? 'phone' : 'desktop'} open onOpenChange={close} returnFocusRef={returnFocusRef}
    onManage={action => {
      binding.current(data);
      const files = new Map<string, FileInfoFileRow>(), candidates: {id: string; occurrence: string; facts: FileFacts}[] = [];
      for (const entry of entries ?? []) {
        if (entry.file) { files.set(entry.file.occurrence, entry.file); candidates.push({id: entry.id, occurrence: entry.file.occurrence, facts: fileInfoFileFacts(entry.file)}); continue; }
        const listed = snapshot.members.get(entry.group!.id)?.rows?.rows ?? [];
        for (const file of listed) files.set(file.occurrence, file);
        if (entry.group!.memberCount === 1) candidates.push({id: entry.id, occurrence: listed[0]?.occurrence ?? entry.head, facts: fileInfoVersionFacts(entry.group!)});
      }
      const whole = lane?.rows && !lane.rows.more && !lane.rows.earlier && entries?.length === 1 ? entries[0]! : undefined;
      const single = whole && (whole.file ? whole.file.occurrence : whole.group!.memberCount === 1 && files.size === 1 ? [...files.keys()][0] : undefined);
      onManage(Object.freeze({action, opening: session.opening.token, surface: snapshot.surface!, files, splitCandidates: Object.freeze(candidates), ...(single ? {sourceOccurrence: single} : {})}));
    }} />;
}
