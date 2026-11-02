import type {ReactNode} from 'react';
import type {EntryMetadataEffect} from '../../shell/entry-metadata';
import {MenuGroup, MenuItem, MenuSeparator} from '../../kit/overlays';
import {useShellWidth} from '../../shell/viewport';
import {runScreenCommand, type ScreenContext} from './context';
import {t} from './text';

export type MetadataTarget = Parameters<EntryMetadataEffect>[0];

/**
 * The ⋯ menu's Owner section (owner-metadata.html frame 6: "Edit metadata… (the ⋯ menu's Owner
 * section)"; tablet.html frame 7). One shared section for every title page that is not the leaf
 * Detail page — show, season, album, artist, book and their rows — so each opens the same editor.
 * Edit metadata… is drawn only where the editor effect exists (a local owner); `children` are the
 * page's other owner rows. `guard` throws when the page the menu was opened for has gone.
 */
export function OwnerMetadataGroup({context, target, anchor, guard, separated = false, children}: Readonly<{
  context: ScreenContext; target?: MetadataTarget; anchor: () => HTMLElement | null | undefined; guard: () => void; separated?: boolean; children?: ReactNode;
}>) {
  const sheet = useShellWidth() < 600, edit = target ? context.effects.editMetadata : undefined;
  if (!edit && !children) return null;
  return <>
    {separated && !sheet && <MenuSeparator />}
    <MenuGroup label={t(sheet ? 'next.overlays.ownerOnly' : 'next.overlays.owner')} ownerOnly>
      {edit && target && <MenuItem icon="i-edit" onSelect={() => runScreenCommand(context, () => {
        guard();
        const element = anchor();
        if (!element) throw new DOMException('Title page changed', 'AbortError');
        return edit(target, element);
      })}>{t('entry.editMetadata')}</MenuItem>}
      {children}
    </MenuGroup>
  </>;
}
