import {defaultI18n} from '@i18n';
import {useId, useRef, useState, type ReactNode, type RefObject} from 'react';
import {Button} from '../controls';
import {Icon} from '../Icon';
import {Menu, MenuGroup, MenuItem, type MenuEntry} from '../overlays';
import {usePhone} from '../overlays/shared';
import {selectionText as text} from './copy';
import {selectionCountText} from './presentation';
import type {SelectionActions, SelectionBarProps, SelectionCount} from './types';
import styles from './selection.module.css';

type ActionMenu = 'queue' | 'watched' | 'more' | null;
type ActionEntry = Exclude<MenuEntry, {section: true} | {separator: true}>;
function useActionMenus(selection: SelectionCount, actions: SelectionActions) {
  const phone = usePhone();
  const [open, setOpen] = useState<ActionMenu>(null);
  const queue = useRef<HTMLButtonElement | null>(null), watched = useRef<HTMLButtonElement | null>(null), more = useRef<HTMLButtonElement | null>(null);
  const extra: ActionEntry[] = [], owner: ActionEntry[] = [];
  if (actions.onCollection) extra.push({id: 'collection', label: text('next.selection.addToCollection'), icon: 'i-layers', onSelect: () => { if (more.current) actions.onCollection!(more.current); }});
  if (actions.onWatchedChange) extra.push({id: 'unwatched', label: text('next.selection.markUnwatched'), icon: 'i-eyeoff-selection', onSelect: () => actions.onWatchedChange!(false)});
  if (actions.onFavorite) extra.push({id: 'favorite', label: text('next.selection.favorite'), icon: 'i-heart', onSelect: actions.onFavorite});
  if (actions.onRemoveFromMyList) extra.push({id: 'remove', label: text('next.selection.removeFromMyList'), icon: 'i-x', onSelect: actions.onRemoveFromMyList});
  if (actions.downloadDisabledReason) extra.push({id: 'download', label: defaultI18n.t('action.download'), icon: 'i-download', disabled: true, description: actions.downloadDisabledReason, onSelect: () => {}});
  if (actions.onRefresh) owner.push({id: 'refresh', label: text('next.selection.refresh'), icon: 'i-refresh', onSelect: actions.onRefresh});
  if (actions.onDelete || actions.deleteDisabledReason) owner.push({id: 'delete', label: text('next.selection.deleteMenu'), icon: 'i-trash', destructive: true, disabled: !!actions.deleteDisabledReason, description: actions.deleteDisabledReason ?? actions.deleteExplanation, onSelect: () => {if (!actions.deleteDisabledReason) actions.onDelete?.();}});
  const ownerLabel = <span className={styles.ownerMenuLabel}>{actions.ownerMark}<span>{text('next.selection.owner')}</span></span>;
  const moreRow = (item: ActionEntry, ownerRow = false) => <MenuItem key={item.id} icon={item.icon} description={item.description} disabled={item.disabled} selected={item.selected} destructive={item.destructive} onSelect={item.onSelect} className={ownerRow ? styles.moreOwnerRow : undefined}>{item.label}</MenuItem>;
  const menus = <>
    <Menu open={open === 'queue'} onOpenChange={value => setOpen(value ? 'queue' : null)} title={text('next.selection.queueTitle', {items: selectionCountText(selection)})} description={phone ? text('next.selection.queueOrder') : undefined} anchorRef={queue} width={330} placement="bottom-start" className={styles.selectionMenu} items={actions.onQueue || actions.queueDisabledReason ? [
      {id: 'next', label: text('next.selection.playNext'), description: actions.queueDisabledReason ?? text(phone ? 'next.selection.playNextPhoneDescription' : 'next.selection.playNextDescription'), disabled: !!actions.queueDisabledReason, icon: 'i-pnext', onSelect: () => {if (!actions.queueDisabledReason) actions.onQueue?.('next');}},
      {id: 'end', label: text('next.selection.playLater'), description: actions.queueDisabledReason ?? text('next.selection.playLaterDescription'), disabled: !!actions.queueDisabledReason, icon: 'i-plater', onSelect: () => {if (!actions.queueDisabledReason) actions.onQueue?.('end');}},
    ] : []} />
    <Menu open={open === 'watched'} onOpenChange={value => setOpen(value ? 'watched' : null)} title={text('next.selection.watchMenu')} anchorRef={watched} width={260} placement="bottom-start" items={actions.onWatchedChange ? [
      {id: 'watched', label: text('next.selection.markWatched'), icon: 'i-check', onSelect: () => actions.onWatchedChange!(true)},
      {id: 'unwatched', label: text('next.selection.markUnwatched'), icon: 'i-eyeoff-selection', onSelect: () => actions.onWatchedChange!(false)},
    ] : []} />
    <Menu open={open === 'more'} onOpenChange={value => setOpen(value ? 'more' : null)} title={text('next.selection.moreActions')} anchorRef={more} width={330} placement="bottom-end" className={styles.selectionMenu}>
      {extra.length > 0 && <MenuGroup>{extra.map(item => moreRow(item))}</MenuGroup>}
      {owner.length > 0 && <MenuGroup label={ownerLabel} className={styles.moreOwnerGroup}>{owner.map(item => moreRow(item, true))}</MenuGroup>}
    </Menu>
  </>;
  const phoneExtra: MenuEntry[] = [...extra, ...(owner.length > 0 ? [{id: 'owner', section: true as const, label: ownerLabel, ownerOnly: true}, ...owner] : [])];
  return {open, setOpen, queue, watched, more, menus, extra: phoneExtra};
}

function SelectAll({selectAll, onSelectAll}: {selectAll: Exclude<SelectionBarProps['selectAll'], undefined>; onSelectAll?: () => void}) {
  const reasonId = useId();
  if (selectAll.kind === 'unavailable') return <span className={styles.selectAllUnavailable}>
    <Button variant="text" className={styles.selectAll} size="small" disabled aria-describedby={reasonId}>{text('next.selection.selectAllUnavailableLabel')}</Button>
    <small id={reasonId}>{selectAll.reason}</small>
  </span>;
  const label = selectAll.kind === 'row' ? text('next.selection.selectRow') : selectAll.kind === 'clear' ? text('next.selection.clear') : text('next.selection.selectAll', {count: selectAll.total});
  return <Button variant="text" className={styles.selectAll} size="small" onClick={onSelectAll}>{label}</Button>;
}

/** Mount in the existing toolbar slot. The shell mounts PhoneActionBar in place of its tab bar. */
export function SelectionBar({selection, actions, context, typeCount, selectAll, onSelectAll, onExit, allInView, sticky = false, className}: SelectionBarProps) {
  const menu = useActionMenus(selection, actions);
  const subtitle = allInView ? text('next.selection.allInView') : context ?? (typeCount && typeCount > 1 ? text('next.selection.typeCount', {count: typeCount}) : undefined);
  if (selection.count <= 0) return null;
  return <div className={[styles.barContainer, sticky && styles.stickyBar, className].filter(Boolean).join(' ')}>
    <div className={styles.desktopBar} role="toolbar" aria-label={text('next.selection.selection')} onKeyDown={event => { if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); onExit(); } }}>
      <Button variant="icon" size="small" iconOnly icon="i-x" iconSize={20} aria-label={text('next.selection.clear')} className={styles.barClose} onClick={onExit} />
      <b className={styles.selectedCount}>{text('next.selection.selected', {count: selection.count})}{subtitle && <small>{subtitle}</small>}</b>
      <span className={styles.barSeparator} aria-hidden="true" />
      {(actions.onPlay || actions.playDisabledReason) && <Button variant="primary" size="compact" icon="i-play" iconSize={18} className={styles.barPrimary} disabled={!!actions.playDisabledReason} title={actions.playDisabledReason} aria-description={actions.playDisabledReason} onClick={actions.playDisabledReason ? undefined : actions.onPlay}>{text('next.selection.play')}</Button>}
      {(actions.onQueue || actions.queueDisabledReason) && <Button size="compact" icon="i-queue" iconSize={18} ref={menu.queue} className={styles.barAction} aria-haspopup="menu" aria-expanded={menu.open === 'queue'} onClick={() => menu.setOpen(menu.open === 'queue' ? null : 'queue')}>{text('next.selection.addToQueue')}<Icon name="i-down" size={16} /></Button>}
      {(actions.onPlaylist || actions.playlistDisabledReason) && <Button size="compact" icon="i-listadd" iconSize={18} className={styles.barAction} disabled={!!actions.playlistDisabledReason} title={actions.playlistDisabledReason} aria-description={actions.playlistDisabledReason} onClick={actions.playlistDisabledReason ? undefined : event => actions.onPlaylist?.(event.currentTarget)}>{text('next.selection.addToPlaylist')}</Button>}
      {actions.onMyList && <Button size="compact" icon="i-plus" iconSize={18} className={styles.barAction} onClick={actions.onMyList}>{text('next.selection.myList')}</Button>}
      {actions.onWatchedChange && <Button size="compact" icon="i-check" iconSize={18} ref={menu.watched} className={styles.barAction} aria-label={text('next.selection.watchMenu')} aria-haspopup="menu" aria-expanded={menu.open === 'watched'} onClick={() => menu.setOpen(menu.open === 'watched' ? null : 'watched')}>{text('next.selection.watched')}<Icon name="i-down" size={16} /></Button>}
      {(actions.onEdit || actions.editDisabledReason) && <Button size="compact" icon="i-edit" iconSize={18} className={[styles.barAction, actions.editMode === 'mixed' ? styles.mixedEdit : styles.ownerEdit].join(' ')} aria-label={text('next.selection.editAccessibility')} disabled={!!actions.editDisabledReason} title={actions.editDisabledReason} aria-description={actions.editDisabledReason} onClick={actions.editDisabledReason ? undefined : event => actions.onEdit?.(event.currentTarget)}>{text('next.selection.edit')}</Button>}
      {menu.extra.length > 0 && <Button variant="icon" size="small" iconOnly icon="i-more" iconSize={20} ref={menu.more} aria-label={text('next.selection.moreActions')} aria-haspopup="menu" aria-expanded={menu.open === 'more'} className={styles.barMore} onClick={() => menu.setOpen(menu.open === 'more' ? null : 'more')} />}
      <div className={styles.barRight}>{selectAll && (selectAll.kind === 'unavailable' || onSelectAll) && <SelectAll selectAll={selectAll} onSelectAll={onSelectAll} />}<Button variant="text" size="small" className={styles.done} onClick={onExit}>{text('next.selection.done')}</Button><kbd className={styles.escape} title={text('next.selection.escHint')}>{text('next.selection.esc')}</kbd></div>
    </div>
    <div className={styles.phoneHeader} role="toolbar" aria-label={text('next.selection.selection')}>
      <Button variant="text" size="small" className={styles.cancelSelection} onClick={onExit}>{text('next.selection.cancel')}</Button>
      <b>{text('next.selection.selected', {count: selection.count})}</b>{selectAll && (selectAll.kind === 'unavailable' || onSelectAll) && <SelectAll selectAll={selectAll} onSelectAll={onSelectAll} />}
      {context && <small>{context}</small>}
    </div>
    {allInView && <div className={styles.allNote}><Icon name="i-check" size={18} /><span><b>{text(allInView.scope === 'view' ? 'next.selection.allSelectedViewNote' : 'next.selection.allSelectedNote', {items: selectionCountText(selection)})}</b> {text('next.selection.allSelectedRemainder', {visible: allInView.visible})}</span><Button variant="text" size="xs" onClick={onExit}>{text('next.selection.clear')}</Button></div>}
    {menu.menus}
  </div>;
}

export type PhoneActionBarProps = Readonly<{selection: SelectionCount; actions: SelectionActions; episodeSelection?: boolean; className?: string}>;
export function PhoneActionBar({selection, actions, episodeSelection = false, className}: PhoneActionBarProps) {
  const menu = useActionMenus(selection, actions);
  if (selection.count <= 0) return null;
  // Phone More includes the actions that have moved out of its five visible slots.
  const phoneExtra: MenuEntry[] = [];
  if (actions.onWatchedChange && !episodeSelection) phoneExtra.push({id: 'mark-watched', label: text('next.selection.markWatched'), icon: 'i-check', onSelect: () => actions.onWatchedChange!(true)});
  if (actions.onWatchedChange) phoneExtra.push({id: 'mark-unwatched', label: text('next.selection.markUnwatched'), icon: 'i-eyeoff-selection', onSelect: () => actions.onWatchedChange!(false)});
  if (episodeSelection && actions.onMyList) phoneExtra.push({id: 'my-list', label: text('next.selection.myList'), icon: 'i-plus', onSelect: actions.onMyList});
  phoneExtra.push(...menu.extra.filter(item => item.id !== 'unwatched'));
  if (actions.onEdit || actions.editDisabledReason) {
    const index = phoneExtra.findIndex(entry => 'section' in entry && entry.id === 'owner');
    const edit: MenuEntry = {id: 'edit', label: text('next.selection.editMenu'), icon: 'i-edit', disabled: !!actions.editDisabledReason, description: actions.editDisabledReason, onSelect: () => { if (!actions.editDisabledReason && menu.more.current) actions.onEdit?.(menu.more.current); }};
    if (index >= 0) phoneExtra.splice(index + 1, 0, edit);
    else phoneExtra.push({id: 'editor', section: true, label: <span className={styles.ownerMenuLabel}>{actions.ownerMark}<span>{text('next.selection.owner')}</span></span>, ownerOnly: true}, edit);
  }
  const action = (label: string, glyph: ReactNode, callback: (() => void) | undefined, ref?: RefObject<HTMLButtonElement | null>, primary = false, expanded?: boolean, reason?: string) => <Button ref={ref} variant="text" size="small" className={[styles.phoneAction, primary && styles.phonePlay].filter(Boolean).join(' ')} aria-haspopup={expanded === undefined ? undefined : 'menu'} aria-expanded={expanded} disabled={!!reason} title={reason} aria-description={reason} onClick={reason ? undefined : callback}><span className={styles.phoneGlyph}>{glyph}</span><span>{label}</span></Button>;
  return <>
    <div className={[styles.phoneActionBar, className].filter(Boolean).join(' ')} role="toolbar" aria-label={text('next.selection.actions')}>
      {(actions.onPlay || actions.playDisabledReason) && action(text('next.selection.play'), <Icon name="i-play" size={19} />, actions.onPlay, undefined, true, undefined, actions.playDisabledReason)}
      {(actions.onQueue || actions.queueDisabledReason) && action(text('next.selection.queue'), <Icon name="i-queue" size={23} />, () => menu.setOpen('queue'), menu.queue, false, menu.open === 'queue')}
      {(actions.onPlaylist || actions.playlistDisabledReason) && <Button variant="text" size="small" className={styles.phoneAction} disabled={!!actions.playlistDisabledReason} title={actions.playlistDisabledReason} aria-description={actions.playlistDisabledReason} onClick={actions.playlistDisabledReason ? undefined : event => actions.onPlaylist?.(event.currentTarget)}><span className={styles.phoneGlyph}><Icon name="i-listadd" size={23} /></span><span>{text('next.selection.playlist')}</span></Button>}
      {episodeSelection && actions.onWatchedChange ? action(text('next.selection.watched'), <Icon name="i-check" size={23} />, () => menu.setOpen('watched'), menu.watched, false, menu.open === 'watched') : actions.onMyList && action(text('next.selection.myList'), <Icon name="i-plus" size={23} />, actions.onMyList)}
      {phoneExtra.length > 0 && action(text('next.selection.more'), <Icon name="i-more" size={23} />, () => menu.setOpen('more'), menu.more, false, menu.open === 'more')}
    </div>
    {menu.open !== 'more' && menu.menus}
    <Menu open={menu.open === 'more'} onOpenChange={value => menu.setOpen(value ? 'more' : null)} title={text('next.selection.selectedItems', {items: selectionCountText(selection)})} anchorRef={menu.more} items={phoneExtra} className={styles.selectionMenu} />
  </>;
}
