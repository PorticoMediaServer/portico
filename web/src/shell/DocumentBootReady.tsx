import {useLayoutEffect, type ReactNode} from 'react';
import {handoverDocumentBoot} from './boot-handover';

/** Called by the first committed shell, public entry or startup-failure page. */
export function DocumentBootReady({ready = true, children}: {ready?: boolean; children?: ReactNode}) {
  useLayoutEffect(() => { if (ready) handoverDocumentBoot(document); }, [ready]);
  return children;
}
