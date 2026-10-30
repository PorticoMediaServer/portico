import {useSyncExternalStore, type RefObject} from 'react';
import type {ReadingController, ReaderErrorCode} from '@portico/reader-core';
import {ReaderDisplayPopover, ReaderDisplaySheet, ReaderDisplaySidePanel, type ReaderDisplayProps} from '../../kit/reader/ReaderDisplay';
import {readingDisplayControls, type ReadingDisplayCopy, type ReadingDisplayScale} from './display';
import type {ReaderMountAuthority} from './mount';

/** The frame decides the surface; the live core owns every control value. */
export function ReadingDisplay({controller, authority, copy, scale, presentation, popover = false, label, open, onOpenChange, anchorRef, onOpenSettings, reportError}: Readonly<{
  controller: ReadingController; authority: ReaderMountAuthority; copy: ReadingDisplayCopy; scale: ReadingDisplayScale;
  presentation: 'phone' | 'tablet' | 'desktop'; popover?: boolean; label: string; open: boolean;
  onOpenChange: ReaderDisplayProps['onOpenChange']; anchorRef: RefObject<HTMLElement | null>;
  onOpenSettings?: () => void; reportError: (code: ReaderErrorCode) => void;
}>) {
  useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const props = readingDisplayControls({controller, authority, copy, scale, phone: presentation === 'phone', reportError});
  const Surface = presentation === 'phone' ? ReaderDisplaySheet : presentation === 'tablet' && !popover ? ReaderDisplaySidePanel : ReaderDisplayPopover;
  return <Surface {...props} label={label} open={open} onOpenChange={onOpenChange} anchorRef={anchorRef}
    onOpenSettings={onOpenSettings} layout={presentation === 'tablet' ? 'tablet' : 'desktop'} />;
}
