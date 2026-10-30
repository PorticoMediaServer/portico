import React, {useEffect, useSyncExternalStore} from 'react';
import {fileInfoTrackKey, type FileInfoLane, type FileInfoSession} from '../../../../../packages/client-core/src/file-info-session';
import type {ManageAction, TextEnvironment} from '../../../../../packages/client-core/src/presentation/kit-model';
import {NativeFileInfoContent, type NativeFileInfoIdentity, type NativeFileInfoMore} from './FileInfoContent';
import {nativeFileInfoShowFile, nativeFileInfoShowVersion, nativeFileInfoView} from './file-info';

export type NativeFileInfoPanelProps = Readonly<{
  session: FileInfoSession; kind: string; text: TextEnvironment; isTV: boolean; identity?: NativeFileInfoIdentity;
  onManage: (action: ManageAction['id'], occurrence?: string) => void;
  /** A lane whose opening went stale cannot be retried as it is; its owner reopens File info. */
  onStale?: () => void;
}>;

/** File info for one opening: versions arrive window by window, and each resident version's
 * offered head tracks (and an owner's files) are read as it becomes resident. Nothing drains a
 * cursor; every further window is one explicit "Show more". */
export function NativeFileInfoPanel({session, kind, text, isTV, identity, onManage, onStale}: NativeFileInfoPanelProps) {
  const snapshot = useSyncExternalStore(session.subscribe, session.getSnapshot, session.getSnapshot);
  const groups = snapshot.groups.rows?.rows, files = snapshot.files?.rows?.rows;
  useEffect(() => {
    for (const group of groups ?? []) nativeFileInfoShowVersion(session, group);
    for (const file of files ?? []) nativeFileInfoShowFile(session, file);
  }, [session, groups, files, snapshot.surface]);
  const view = nativeFileInfoView(snapshot, kind, text, isTV);
  if (!view) return null;
  const more = <Row,>(lane: FileInfoLane<Row> | undefined, next: () => void): NativeFileInfoMore | undefined =>
    lane && (lane.rows?.more || lane.status === 'failed') ? {busy: lane.status === 'loading' || lane.status === 'preparing', failed: lane.status === 'failed',
      onPress: lane.status === 'failed' && (lane.error as {code?: string} | undefined)?.code === 'stale_continuation' && onStale ? onStale : next} : undefined;
  const byId = new Map(groups?.map(group => [group.id, group]));
  return <NativeFileInfoContent model={view.model} locations={view.locations} disc={view.disc} identity={identity} versionCount={view.versionCount} onManage={onManage}
    more={files ? more(snapshot.files, () => { session.files(snapshot.files?.rows?.more ? 'next' : 'restart'); }) : more(snapshot.groups, () => { session.groups(snapshot.groups.rows?.more ? 'next' : 'restart'); })}
    earlier={(files ? snapshot.files?.rows?.earlier : snapshot.groups.rows?.earlier) ? {busy: false, failed: false, onPress: () => { (files ? session.files('restart') : session.groups('restart')); }} : undefined}
    trackMore={id => {
      // A flat file's tracks are its own raw lanes.
      const file = files?.find(row => row.occurrence === id);
      if (file) for (const lane of ['audio', 'subtitles'] as const) {
        const held = snapshot.tracks.get(fileInfoTrackKey(id, lane));
        const next = more(held, () => { session.tracks(id, lane, held?.rows?.more ? 'next' : 'open'); });
        if (next) return next;
      }
      const group = byId.get(id);
      if (!group) return undefined;
      // Audio first, then subtitles: one continuation at a time, each from its own lane.
      for (const lane of ['audio', 'subtitles'] as const) {
        const held = snapshot.headTracks.get(fileInfoTrackKey(id, lane));
        const next = more(held, () => { session.headTracks(id, lane, held?.rows?.more ? 'next' : 'open'); });
        if (next) return next;
      }
      return undefined;
    }}
    filesMore={id => more(snapshot.members.get(id), () => { session.members(id, snapshot.members.get(id)?.rows?.more ? 'next' : 'open'); })} />;
}
