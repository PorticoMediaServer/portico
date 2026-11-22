import React, {useSyncExternalStore} from 'react';
import {useDockClearance} from '../dock/DockClearance';
import {ToastHost} from './Feedback';
import type {ToastMessage} from './types';

/** A screen feedback service's toast, mounted once in the genuine shared overlay outlet, clear of the dock. */
export function NativeScreenFeedbackHost({service, bottomOffset}: {service: Readonly<{subscribe: (listener: () => void) => () => void;
  getSnapshot: () => ToastMessage | null; dismiss: (id: string) => void}>; bottomOffset?: number}) {
  const toast = useSyncExternalStore(service.subscribe, service.getSnapshot);
  const dockTop = useDockClearance();
  return <ToastHost toast={toast} onDismiss={service.dismiss} bottomOffset={bottomOffset} obstructionTop={dockTop}/>;
}
