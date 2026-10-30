import {useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {DetailService, type DetailProjection} from '@core/detail.ts';
import {MetadataRepairService, type RepairData} from '@core/metadata-repair.ts';
import {defaultI18n} from '@i18n';
import {Dialog} from '../kit/overlays';
import {MetadataEditor} from '../screens/metadata/MetadataEditor';
import styles from '../screens/metadata/metadata.module.css';
import type {ShellArtwork} from './artwork';
import type {EntryActionPorts, EntryActionTarget} from './entry-actions';

export type EntryMetadataPorts = Omit<EntryActionPorts, 'api'> & Readonly<{artwork?: ShellArtwork; api: EntryActionPorts['api'] & Readonly<{requestMetadataUpload?: <T>(path: string, body: FormData, signal?: AbortSignal) => Promise<T>}>}>;
type AggregateMetadataTarget = Readonly<{entityKind: 'show' | 'album' | 'book'; entityId: string; libraryId: string; title: string; initialTab?: 'general' | 'matching'}>;
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

function CheckedMetadata({value}: {value: Open & {target: EntryActionTarget}}) {
  const anchor = useRef<HTMLElement | null>(value.anchor);
  const [data, setData] = useState<DetailProjection>();
  const [failed, setFailed] = useState(false);
  const api = useMemo(() => ({async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    const check = () => {if (!current(value)) throw new DOMException('Metadata authority changed', 'AbortError');};
    check();
    const result = await value.ports.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, value.lifetime.signal]) : value.lifetime.signal);
    check(); return result;
  }}), [value]);
  useEffect(() => {
    const service = new DetailService({api, scope: {serverId: value.ports.serverId, viewerId: value.ports.viewerId}, requestId: value.ports.requestId});
    void service.select({libraryId: value.target.libraryId, itemId: value.target.itemId}).then(() => {
      if (!current(value)) return;
      const snapshot = service.getSnapshot(), detail = snapshot.data;
      if (snapshot.phase !== 'ready' || !detail || detail.item.id !== value.target.itemId || detail.scope.libraryId !== value.target.libraryId
        || detail.item.kind !== value.target.kind || !['movie', 'episode', 'song', 'audiobook_file'].includes(detail.item.kind) || !detail.actions.some(action => action.id === 'metadata_edit' && action.enabled)) {setFailed(true); return;}
      setData(detail);
    }).catch(() => {if (current(value)) setFailed(true);});
    return () => {service.dispose(); value.lifetime.abort();};
  }, [api, value]);
  const metadataApi = useMemo(() => ({async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    if (!data || !current(value)) throw new DOMException('Metadata grant retired', 'AbortError');
    const result = await api.request<T>(path, method, body, signal);
    if (!current(value)) throw new DOMException('Metadata grant retired', 'AbortError');
    // Every full editing document must retain the fresh Detail viewer fence.
    if (result && typeof result === 'object' && 'viewerFence' in result && result.viewerFence !== data.scope.viewerFence) throw new Error('metadata-fence-mismatch');
    return result;
  }, ...(value.ports.api.requestMetadataUpload ? {async requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T> {
    if (!data || !current(value)) throw new DOMException('Metadata grant retired', 'AbortError');
    const result = await value.ports.api.requestMetadataUpload!<T>(path, body, signal ? AbortSignal.any([signal, value.lifetime.signal]) : value.lifetime.signal);
    if (!current(value)) throw new DOMException('Metadata grant retired', 'AbortError');
    if (result && typeof result === 'object' && 'viewerFence' in result && result.viewerFence !== data.scope.viewerFence) throw new Error('metadata-fence-mismatch');
    return result;
  }} : {})}), [api, data, value]);
  const scope = useMemo(() => ({serverId: value.ports.serverId, viewerId: value.ports.viewerId}), [value]);
  const target = useMemo(() => ({kind: 'item' as const, id: value.target.itemId, libraryId: value.target.libraryId}), [value]);
  return <Dialog open responsive={false} onOpenChange={open => {if (!open) close();}} title={defaultI18n.t('next.home.editSelection')}
    returnFocusRef={anchor} placement="right" width={720} showCloseButton={!data} headerClassName={data ? styles.hostTitle : undefined} bodyClassName={data ? styles.hostBody : undefined}>
    {data ? <MetadataEditor artwork={value.ports.artwork} api={metadataApi} scope={scope} target={target} onClose={close}/>
      : <p role={failed ? 'alert' : 'status'}>{defaultI18n.t(failed ? 'next.states.crashed' : 'next.states.loading')}</p>}
  </Dialog>;
}

function CheckedAggregateMetadata({value}: {value: Open & {target: AggregateMetadataTarget}}) {
  const anchor = useRef<HTMLElement | null>(value.anchor);
  const [grant, setGrant] = useState<RepairData>();
  const [failed, setFailed] = useState(false);
  const scope = useMemo(() => ({serverId: value.ports.serverId, viewerId: value.ports.viewerId}), [value]);
  const target = useMemo(() => ({kind: value.target.entityKind, id: value.target.entityId, libraryId: value.target.libraryId}), [value]);
  const api = useMemo(() => ({async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
    if (!current(value)) throw new DOMException('Metadata authority changed', 'AbortError');
    const result = await value.ports.api.request<T>(path, method, body, signal ? AbortSignal.any([signal, value.lifetime.signal]) : value.lifetime.signal);
    if (!current(value)) throw new DOMException('Metadata authority changed', 'AbortError');
    return result;
  }}), [value]);
  useEffect(() => {
    // The owner-only repair endpoint validates the exact aggregate and library before editing.
    const service = new MetadataRepairService({api, scope});
    void service.load(target).then(() => {
      if (!current(value)) return;
      const snapshot = service.getSnapshot();
      if (snapshot.phase !== 'ready' || !snapshot.data) {setFailed(true); return;}
      setGrant(snapshot.data);
    });
    return () => {service.dispose(); value.lifetime.abort();};
  }, [api, scope, target, value]);
  const metadataApi = useMemo(() => {
    const check = () => {if (!grant || !current(value)) throw new DOMException('Metadata grant retired', 'AbortError');};
    const checked = <T,>(result: T) => {
      if (!grant || !current(value)) throw new DOMException('Metadata grant retired', 'AbortError');
      if (result && typeof result === 'object' && 'viewerFence' in result && result.viewerFence !== grant.viewerFence) throw new Error('metadata-fence-mismatch');
      return result;
    };
    return {async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
      check(); return checked(await api.request<T>(path, method, body, signal));
    }, ...(value.ports.api.requestMetadataUpload ? {async requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T> {
      check();
      return checked(await value.ports.api.requestMetadataUpload!<T>(path, body, signal ? AbortSignal.any([signal, value.lifetime.signal]) : value.lifetime.signal));
    }} : {})};
  }, [api, grant, value]);
  const dismiss = () => {if (opened === value) close();};
  return <Dialog open responsive={false} onOpenChange={open => {if (!open) dismiss();}} title={defaultI18n.t('next.home.editSelection')}
    returnFocusRef={anchor} placement="right" width={720} showCloseButton={!grant} headerClassName={grant ? styles.hostTitle : undefined} bodyClassName={grant ? styles.hostBody : undefined}>
    {grant ? <MetadataEditor artwork={value.ports.artwork} api={metadataApi} scope={scope} target={target} initialTab={value.target.initialTab} onClose={dismiss}/>
      : <p role={failed ? 'alert' : 'status'}>{defaultI18n.t(failed ? 'next.states.crashed' : 'next.states.loading')}</p>}
  </Dialog>;
}

const aggregateOpen = (value: Open): value is Open & {target: AggregateMetadataTarget} => 'entityKind' in value.target;
const itemOpen = (value: Open): value is Open & {target: EntryActionTarget} => 'itemId' in value.target;

export function EntryMetadataHost() {
  const value = useSyncExternalStore(listener => {listeners.add(listener); return () => {listeners.delete(listener);};}, () => opened, () => null);
  if (!value || !current(value)) return null;
  if (aggregateOpen(value)) return <CheckedAggregateMetadata key={value.id} value={value}/>;
  return itemOpen(value) ? <CheckedMetadata key={value.id} value={value}/> : null;
}
