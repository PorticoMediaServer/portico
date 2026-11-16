import {useEffect, useState, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import type {RepairTarget} from '@core/metadata-repair.ts';
import {Dialog} from '../../kit/overlays';
import {Skeleton, StateView} from '../../kit/states';
import type {ReadBindingContext} from './bindings';
import {ReadCoverPages} from './ReadCoverPages';
import {MetadataEditor} from '../metadata/MetadataEditor';
import {screenLifetime, type ScreenContext} from '../detail/context';

/** A reading item's editor: the same editor as every title, with local artwork only. */
export function ReadMetadata({context, itemId, libraryId: expectedLibraryId, onClose, anchor, reading, sourceId, format}: {context: ScreenContext; itemId: string; libraryId?: string; reading?: ReadBindingContext; sourceId?: string; format?: string; onClose: () => void; anchor: RefObject<HTMLElement | null>}) {
  const [ready, setReady] = useState<Readonly<{target: RepairTarget; life: ReturnType<typeof screenLifetime>; api: Parameters<typeof MetadataEditor>[0]['api']}>>(), [failed, setFailed] = useState(false), [retry, setRetry] = useState(0);
  useEffect(() => {
    let active = true; const life = screenLifetime(context);
    setReady(undefined); setFailed(false);
    const viewer = context.identity?.viewer;
    if (viewer?.authority !== 'local' || viewer.role !== 'owner') {life.dispose(); return;}
    // The editor's own read is the check: the rebuilt server answers it only for an owner or administrator.
    const selected: RepairTarget = Object.freeze({kind: 'item', id: itemId, libraryId: expectedLibraryId ?? ''});
    const api: Parameters<typeof MetadataEditor>[0]['api'] = {...life.api,
      async requestRaw(path, method, options) {
        life.assert(); const request = life.signal(options.signal); request.throwIfAborted();
        const result = await context.api.requestRaw(path, method, {...options, signal: request});
        life.assert(); request.throwIfAborted(); return result;
      },
      async requestMetadataUpload<T>(path: string, body: Blob, signal?: AbortSignal): Promise<T> {
        life.assert(); const request = life.signal(signal); request.throwIfAborted();
        const result = await context.api.requestMetadataUpload<T>(path, body, request);
        life.assert(); request.throwIfAborted(); return result;
      },
    };
    if (active) setReady({target: selected, life, api});
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
