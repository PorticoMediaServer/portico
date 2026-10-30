import {useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {FactPart} from '@core/presentation/kit-model';
import {Button} from '../controls';
import {Menu, MenuGroup, MenuItem} from '../overlays';
import {Icon} from '../Icon';
import type {BrowsePlatform} from './types';
import styles from './browse.module.css';

const t = defaultI18n.t;
export type LibraryHeaderProps = Readonly<{
  name: string; counts?: readonly FactPart[]; switcher: ReactNode; menu?: ReactNode;
  ambient?: ReactNode; children?: ReactNode; platform?: BrowsePlatform; className?: string;
}>;
export function LibraryHeader({name, counts, switcher, menu, ambient, children, platform = 'desktop', className}: LibraryHeaderProps) {
  return <header className={[styles.libraryHeader, className].filter(Boolean).join(' ')} data-platform={platform} data-summary={!!counts?.length || undefined}>
    {ambient && <div className={styles.headerAmbient} aria-hidden="true">{ambient}</div>}
    <div className={styles.libraryIdentity}><div><div className={styles.libraryName}><h1>{name}</h1>{switcher}</div>
      {!!counts?.length && <p className={styles.libraryCounts}>{counts.map(part => <span key={part.id}>{part.text}</span>)}</p>}</div>
      {menu && <div className={styles.libraryEnd}>{menu}</div>}</div>{children}
  </header>;
}
export type StickyBrowseHeaderProps = Omit<LibraryHeaderProps, 'counts' | 'platform'> & Readonly<{onLibraries: () => void; sticky?: boolean; topInset?: number}>;
export function StickyBrowseHeader({name, switcher, menu, ambient, children, onLibraries, sticky = true, topInset = 0, className}: StickyBrowseHeaderProps) {
  return <header className={[styles.phoneHeader, className].filter(Boolean).join(' ')} data-sticky={sticky || undefined} style={{top: topInset}}>
    {ambient && <div className={styles.headerAmbient} aria-hidden="true">{ambient}</div>}
    <div className={styles.phoneHeaderRow}><Button variant="text" size="small" icon="i-left" onClick={onLibraries}>{t('next.browse.libraries')}</Button>
      <div className={styles.phoneSwitcher} aria-label={name}>{switcher}</div><div className={styles.phoneMenu}>{menu}</div></div>{children}
  </header>;
}
export type LibraryChoice = Readonly<{id: string; name: string; count?: ReactNode}>;
export type LibrarySwitcherProps = Readonly<{
  libraries: readonly LibraryChoice[]; value: string; onValueChange: (id: string) => void;
  open?: boolean; onOpenChange?: (open: boolean) => void; compact?: boolean; className?: string;
}>;
export function LibrarySwitcher({libraries, value, onValueChange, open: controlledOpen, onOpenChange, compact = false, className}: LibrarySwitcherProps) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [localOpen, setLocalOpen] = useState(false), open = controlledOpen ?? localOpen;
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next); };
  const name = libraries.find(library => library.id === value)?.name;
  return <Menu returnFocusRef={triggerRef} presentation="anchored" open={open} onOpenChange={setOpen} title={t('next.browse.switchLibrary')} accessibilityLabel={t('next.browse.switchLibrary')}
    placement="bottom-start" width={252} className={styles.librarySwitcherMenu}
    trigger={<button ref={triggerRef} type="button" className={[compact ? styles.compactSwitcher : styles.roundSwitcher, className].filter(Boolean).join(' ')}
      aria-label={t('next.browse.switchLibrary')} aria-expanded={open}>{compact && <span>{name}</span>}<Icon name="i-down" size={compact ? 16 : 22}/></button>}>
    {libraries.map(library => <MenuItem key={library.id} className={styles.libraryChoice} selected={library.id === value} description={library.count} onSelect={() => onValueChange(library.id)}>{library.name}</MenuItem>)}
  </Menu>;
}
export type LibraryMenuProps = Readonly<{
  /** Each row is drawn only when the surface has a real port for it. */
  read?: boolean; onSaveView?: () => void; onMarkAll?: () => void; onEditPins?: () => void;
  /** O-161: copy this library's shareable address. */
  onCopyLink?: () => void;
  viewSummary?: ReactNode; countSummary?: ReactNode; ownerMark?: ReactNode; open?: boolean; onOpenChange?: (open: boolean) => void;
  owner: boolean;
  /** Owner-only rows; the owner group is drawn only when one of them exists. */
  onUnmatched?: () => void; onLibrarySettings?: () => void; onScan?: () => void;
}>;
export function LibraryMenu({owner, read = false, onSaveView, onMarkAll, onEditPins, onCopyLink, onUnmatched, onLibrarySettings, onScan, viewSummary, countSummary, ownerMark, open: controlledOpen, onOpenChange}: LibraryMenuProps) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [localOpen, setLocalOpen] = useState(false), open = controlledOpen ?? localOpen;
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next); };
  const act = (callback: () => void) => () => { setOpen(false); callback(); };
  return <Menu returnFocusRef={triggerRef} presentation="anchored" open={open} onOpenChange={setOpen} title={t('next.browse.libraryOptions')} accessibilityLabel={t('next.browse.libraryOptions')}
    placement="bottom-end" width={282} className={styles.libraryMenu}
    trigger={<Button ref={triggerRef} variant="icon" size="compact" icon="i-more" iconOnly className={styles.libraryMenuTrigger} aria-expanded={open} aria-label={t('next.browse.libraryOptions')}/>}>
    {onSaveView && <MenuItem icon={read ? 'i-save' : 'i-filter'} description={viewSummary} onSelect={act(onSaveView)}>{t('next.browse.saveThisView')}</MenuItem>}
    {onMarkAll && <MenuItem icon="i-check" description={countSummary} onSelect={act(onMarkAll)}>{t(read ? 'next.browse.markAllRead' : 'next.browse.markAllWatched')}</MenuItem>}
    {onEditPins && <MenuItem icon="i-updown" onSelect={act(onEditPins)}>{t('next.browse.editPins')}</MenuItem>}
    {onCopyLink && <MenuItem icon="i-copy" onSelect={act(onCopyLink)}>{t('next.overlays.copyLink')}</MenuItem>}
    {owner && (onUnmatched || onLibrarySettings || onScan) && <MenuGroup ownerOnly className={styles.ownerGroup} label={<span className={styles.ownerLabel}>{t('next.browse.server')}{ownerMark}</span>}>
      {onUnmatched && <MenuItem icon="i-list" onSelect={act(onUnmatched)}>{t('next.ownerLibraries.tab.attention')}</MenuItem>}
      {onLibrarySettings && <MenuItem icon="i-cog" onSelect={act(onLibrarySettings)}>{t('next.browse.librarySettings')}</MenuItem>}
      {onScan && <MenuItem icon="i-reload" onSelect={act(onScan)}>{t('next.browse.scan')}</MenuItem>}
    </MenuGroup>}
  </Menu>;
}
