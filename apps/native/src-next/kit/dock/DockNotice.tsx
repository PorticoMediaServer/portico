import React from 'react';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import {clockText} from '../../../../../packages/client-core/src/presentation/kit-model/text';
import {ToastHost} from '../overlays';
import type {DockCommand, DockNoticeModel} from './types';

/** The adapter supplies the actual transition/bookmark; routine playback prompts cannot enter. */
export function DockNotice({notice, onAction, onDismiss, bottomOffset = 154}: {notice: DockNoticeModel | null; onAction: (command: DockCommand) => void; onDismiss: () => void; bottomOffset?: number}) {
  const t = defaultI18n.t;
  const position = notice ? clockText(notice.positionSeconds) : undefined;
  const toast = notice ? {id: notice.id,
    message: notice.kind === 'paused-audio' ? t('next.dock.native.musicPaused', {title: notice.identity.title}) : t('next.dock.native.videoStopped', {title: notice.identity.title, position: position ?? ''}),
    durationMs: notice.kind === 'paused-audio' ? 4000 : 8000,
    ...(notice.kind === 'stopped-video' ? {action: {label: t('entry.resume'), onPress: () => onAction({type: 'restore-video', fence: notice.fence})}} : {}),
  } : null;
  return <ToastHost toast={toast} onDismiss={onDismiss} bottomOffset={bottomOffset}/>;
}
