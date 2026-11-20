import {useLayoutEffect, useState} from 'react';
import type {ReadingManifest} from '../../../../../packages/client-core/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {linkNativeAbortSignals} from '../../platform/abort-signals';
import {nativeScreenServices} from '../native-screen-services';
import {isReadingLibraryKind} from './platform';
import type {NativeReadBook, NativeReadOwnerEditLease} from './read-book-model';
import {nativeReadingCollectionCapability, type NativeReadingCollectionKind} from './read-collection-capability';

/** Whether a book's page may offer Add to collection, and for which kind: asked of the exact item's Detail offer for this
 * committed page under a real Reading library, never from a format or a library spelling. The producer's admitted
 * ebook/comic kind is kept with the lease and manifest it was asked under, and offered only while both are still the
 * page's. Shared by the old reading screen and the rebuilt phone page. */
export function useReadingCollectionKind({context, controller, libraryId, manifest, editLease}: Readonly<{
  context: NativeShellBootstrapContext; controller: NativeReadBook; libraryId: string; manifest?: ReadingManifest; editLease?: NativeReadOwnerEditLease;
}>): NativeReadingCollectionKind | undefined {
  const [capability, setCapability] = useState<Readonly<{lease: NativeReadOwnerEditLease; manifest: ReadingManifest; kind?: NativeReadingCollectionKind}>>();
  const library = context.libraries().find(value => value.id === libraryId);
  const leaseSignal = editLease?.signal, leaseOrigin = editLease?.origin;
  useLayoutEffect(() => {
    const lease = editLease;
    if (!lease || !manifest || !library || !isReadingLibraryKind(library.kind) || !lease.isCurrent()) return;
    const life = new AbortController(), linked = linkNativeAbortSignals([lease.signal, life.signal]);
    const current = () => !linked.signal.aborted && lease.isCurrent() && context.current();
    const services = (() => {try {return nativeScreenServices(context);} catch {return undefined;}})();
    if (!services) return;
    const scope = {serverId: lease.viewer.serverId, viewerId: JSON.stringify([lease.viewer.authority, lease.viewer.accountId, lease.viewer.profileId])};
    void nativeReadingCollectionCapability({api: context.api, scope, requestId: services.requestId, signal: linked.signal,
      isCurrent: current, itemId: controller.item.id, libraryId})
      .then(kind => {if (current()) setCapability({lease, manifest, kind});})
      .catch(() => {if (current()) setCapability({lease, manifest});});
    return () => {life.abort(); linked.dispose();};
  }, [context, controller, manifest, libraryId, library?.id, library?.kind, leaseSignal, leaseOrigin]); // eslint-disable-line react-hooks/exhaustive-deps
  return capability && editLease && library && isReadingLibraryKind(library.kind)
    && capability.manifest === manifest && capability.lease.signal === editLease.signal
    && capability.lease.isCurrent() ? capability.kind : undefined;
}
