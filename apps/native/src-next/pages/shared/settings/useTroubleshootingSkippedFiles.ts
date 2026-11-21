import {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {readSkippedFiles, type SkippedFile} from '../../../../../../packages/client-core/src/admin/skipped-files';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import {NativeScreenLifetime} from '../../../screens/settings-live/lifetime';
import {messagesReader, troubleshootingApi} from './useTroubleshootingMessages';

type Read = Readonly<{items: readonly SkippedFile[]; next?: string; loading: boolean; failed: boolean}>;

/** Files Portico could not prepare (console rules 1 and 72), for the owner and administrators: an administrator sees
 *  those of the libraries they are allowed. Each file with its library and the server's reason. */
export function useSkippedFiles(ports: NativeSettingsLivePorts) {
  const context = ports.context;
  const [lifetime, setLifetime] = useState<NativeScreenLifetime>();
  const [read, setRead] = useState<Read>({items: [], loading: true, failed: false});
  useLayoutEffect(() => {const next = new NativeScreenLifetime(context); setLifetime(next); return () => next.dispose();}, [context]);
  const load = (life: NativeScreenLifetime, cursor?: string) => {
    const current = () => life.current() && messagesReader(context);
    if (!current()) return;
    setRead(before => ({...before, loading: true, failed: false}));
    const api = troubleshootingApi(context, life, current);
    void readSkippedFiles(api, cursor).then(page => {if (current()) setRead(before => ({items: cursor ? [...before.items, ...page.items] : page.items, next: page.nextCursor, loading: false, failed: false}));},
      () => {if (current()) setRead(before => ({...before, loading: false, failed: true}));});
  };
  // The first read goes through the latest `load`, published at commit.
  const ports_ = useRef({load});
  useEffect(() => {ports_.current = {load};});
  useEffect(() => {if (lifetime) ports_.current.load(lifetime);}, [lifetime]);
  return {read, next: () => {if (lifetime) load(lifetime, read.next);}};
}
