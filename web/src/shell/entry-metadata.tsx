import {useMemo, useRef, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {Dialog} from '../kit/overlays';
import {MetadataEditor} from '../screens/metadata/MetadataEditor';
import styles from '../screens/metadata/metadata.module.css';
import type {ShellArtwork} from './artwork';
import type {EntryActionPorts, EntryActionTarget} from './entry-actions';

type RequestRaw = (path: string, method: string, options: Readonly<{headers?: Readonly<Record<string, string>>; body?: unknown; signal?: AbortSignal}>) => Promise<{status: number; body?: unknown}>;
export type EntryMetadataPorts = Omit<EntryActionPorts, 'api'> & Readonly<{artwork?: ShellArtwork; api: EntryActionPorts['api'] & Readonly<{requestRaw?: RequestRaw; requestMetadataUpload?: <T>(path: string, body: Blob, signal?: AbortSignal) => Promise<T>}>}>;
/** A parent title (show, season, album, artist, book), opened from its page, the title menu or Unmatched items. */
export type AggregateMetadataTarget = Readonly<{entityKind: 'show' | 'season' | 'album' | 'artist' | 'book'; entityId: string; libraryId: string; title: string; initialTab?: 'general' | 'matching'}>;
export type EntryMetadataEffect = (target: EntryActionTarget | AggregateMetadataTarget, anchor: HTMLElement) => Promise<void>;
type Open = Readonly<{id: number; target: EntryActionTarget | AggregateMetadataTarget; anchor: HTMLElement; ports: EntryMetadataPorts; lifetime: AbortController; ownerCurrent: () => boolean}>;
let opened: Open | null = null;
let serial = 0;
const listeners = new Set<() => void>();
const emit = () => {for (const listener of listeners) listener();};
const close = () => {opened?.lifetime.abort(); opened = null; emit();};
const current = (value: Open) => opened === value && !value.lifetime.signal.aborted && value.ports.isCurrent() && value.ownerCurrent();

/** The caller's local owner epoch is rechecked for every read and write. */
export function createEntryMetadataEffect(ports: EntryMetadataPorts, ownerCurrent: () => boolean): EntryMetadataEffect {
  return async (target, anchor) => {
    if (!ports.isCurrent() || !ownerCurrent()) throw new DOMException('Metadata owner changed', 'AbortError');
    close();
    opened = Object.freeze({id: ++serial, target: Object.freeze({...target}), anchor, ports, ownerCurrent, lifetime: new AbortController()});
    emit();
  };
}
export function retireEntryMetadata(ports: EntryActionPorts) {if (opened?.ports === ports) close();}

/** One opening of the editor. The server's own read decides whether this person may edit the title (an
 * owner or administrator); every request passes only while this opening is the current one. */
function OpenedMetadata({value}: {value: Open}) {
  const anchor = useRef<HTMLElement | null>(value.anchor);
  const api = useMemo(() => {
    const check = () => {if (!current(value)) throw new DOMException('Metadata authority changed', 'AbortError');};
    const signal = (input?: AbortSignal) => input ? AbortSignal.any([input, value.lifetime.signal]) : value.lifetime.signal;
    const raw = value.ports.api.requestRaw, upload = value.ports.api.requestMetadataUpload;
    return {
      async request<T>(path: string, method?: string, body?: unknown, input?: AbortSignal): Promise<T> {
        check(); const result = await value.ports.api.request<T>(path, method, body, signal(input)); check(); return result;
      },
      ...(raw ? {async requestRaw(path: string, method: string, options: Parameters<RequestRaw>[2]) {
        check(); const result = await raw.call(value.ports.api, path, method, {...options, signal: signal(options.signal)}); check(); return result;
      }} : {}),
      ...(upload ? {async requestMetadataUpload<T>(path: string, body: Blob, input?: AbortSignal): Promise<T> {
        check(); const result = await upload.call(value.ports.api, path, body, signal(input)) as T; check(); return result;
      }} : {}),
    };
  }, [value]);
  const scope = useMemo(() => ({serverId: value.ports.serverId, viewerId: value.ports.viewerId}), [value]);
  const target = useMemo(() => 'entityKind' in value.target
    ? {kind: value.target.entityKind, id: value.target.entityId, libraryId: value.target.libraryId}
    : {kind: 'item' as const, id: value.target.itemId, libraryId: value.target.libraryId}, [value]);
  const initialTab = 'entityKind' in value.target ? value.target.initialTab : undefined;
  return <Dialog open responsive={false} onOpenChange={open => {if (!open && opened === value) close();}} title={defaultI18n.t('next.home.editSelection')}
    returnFocusRef={anchor} placement="right" width={720} headerClassName={styles.hostTitle} bodyClassName={styles.hostBody}>
    <MetadataEditor artwork={value.ports.artwork} api={api} scope={scope} target={target} initialTab={initialTab} onClose={() => {if (opened === value) close();}}/>
  </Dialog>;
}

export function EntryMetadataHost() {
  const value = useSyncExternalStore(listener => {listeners.add(listener); return () => {listeners.delete(listener);};}, () => opened, () => null);
  if (!value || !current(value)) return null;
  return <OpenedMetadata key={value.id} value={value}/>;
}
