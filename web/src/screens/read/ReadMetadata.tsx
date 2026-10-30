import {useEffect, useState, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {encodedRoutePath} from '../../../../packages/contracts/src/routes.ts';
import {validateRepair, type RepairTarget} from '@core/metadata-repair.ts';
import {Dialog} from '../../kit/overlays';
import {Skeleton, StateView} from '../../kit/states';
import type {ReadBindingContext} from './bindings';
import {ReadCoverPages} from './ReadCoverPages';
import {MetadataEditor} from '../metadata/MetadataEditor';
import {screenLifetime, type ScreenContext} from '../detail/context';

/** Reading items are nonplayable: resolve their library through the existing owner metadata response. */
export function ReadMetadata({context, itemId, libraryId: expectedLibraryId, onClose, anchor, reading, sourceId, format}: {context: ScreenContext; itemId: string; libraryId?: string; reading?: ReadBindingContext; sourceId?: string; format?: string; onClose: () => void; anchor: RefObject<HTMLElement | null>}) {
  const [ready, setReady] = useState<Readonly<{target: RepairTarget; life: ReturnType<typeof screenLifetime>; api: Parameters<typeof MetadataEditor>[0]['api']}>>(), [failed, setFailed] = useState(false), [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true; const life = screenLifetime(context);
    setReady(undefined); setFailed(false);
    const viewer = context.identity?.viewer;
    if (viewer?.authority !== 'local' || viewer.role !== 'owner') {life.dispose(); return;}
    void life.api.request<unknown>(encodedRoutePath('get_metadata_kind_id', {kind: 'item', id: encodeURIComponent(itemId)}), 'GET', undefined, life.signal()).then(raw => {
      if (!active || !life.current()) return;
      const libraryId = (raw as {libraryId?: unknown} | null)?.libraryId;
      if (typeof libraryId !== 'string' || !libraryId || libraryId.length > 256 || expectedLibraryId !== undefined && libraryId !== expectedLibraryId) throw new Error('invalid_metadata_target');
      const selected: RepairTarget = Object.freeze({kind: 'item', id: itemId, libraryId});
      validateRepair(raw, context.scope, selected);
      const api: Parameters<typeof MetadataEditor>[0]['api'] = {...life.api,
        async requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T> {
          life.assert(); const request = life.signal(signal); request.throwIfAborted();
          const result = await context.api.requestMetadataUpload<T>(path, body, request);
          life.assert(); request.throwIfAborted(); return result;
        },
      };
      setReady({target: selected, life, api});
    }).catch(error => {if (active && life.current()) {setFailed(true); context.effects.reportError(error);}});
    return () => {active = false; life.dispose();};
  }, [context, itemId, expectedLibraryId, retry]);
  const owner = context.identity?.viewer;
  if (!context.isCurrent() || owner?.authority !== 'local' || owner.role !== 'owner') return null;
  return <Dialog open placement="right" width={520} title={defaultI18n.t('entry.editMetadata')} onOpenChange={open => {if (!open) onClose();}} returnFocusRef={anchor}>
    {ready ? <MetadataEditor localArtworkOnly renderCoverPages={reading && sourceId && format === 'cbz' ? (upload, busy) => <ReadCoverPages reading={reading} context={context} itemId={itemId} sourceId={sourceId} upload={upload} busy={busy}/> : undefined} api={ready.api} scope={context.scope} target={ready.target} onClose={onClose}/>
      : failed ? <StateView title={defaultI18n.t('next.states.pageFailed')} body={defaultI18n.t('next.states.pageFailedBody')}
        primary={{id: 'retry', label: defaultI18n.t('next.states.tryAgain'), onPress: () => setRetry(value => value + 1)}}/> : <Skeleton quiet height={480}/>}
  </Dialog>;
}
