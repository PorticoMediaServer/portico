import {useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode, type Ref} from 'react';
import {defaultI18n} from '@i18n';
import {Button, Select, TextField} from '../controls';
import {Icon} from '../Icon';
import {Artwork} from '../artwork/Artwork';
import type {ArtworkSource} from '../artwork/types';
import {accountAvatarStyle} from './account-avatar';
import {nextTableSort, selectTableRows, settingsTableHeaderHeight, settingsTableRowHeight, settingsTableWindow, tableFocusFallback, type TableQuery} from './table-model';
import styles from './settings.module.css';

const t = defaultI18n.t;
export type SettingsTableCell = string | Readonly<{title: string; meta?: string; avatar?: string; avatarKey?: string; image?: string; artwork?: ArtworkSource; tone?: 'ok' | 'accent' | 'warn' | 'bad' | 'quiet'}>;
export type SettingsTableRow = Readonly<{id: string; label: string; selectable?: boolean; cells: Readonly<Record<string, SettingsTableCell>>}>;
export type SettingsTableColumn = Readonly<{id: string; label: string; kind?: 'main' | 'text' | 'muted' | 'badge' | 'num'; sortable?: boolean; width?: string}>;
export type SettingsTableFilter = Readonly<{id: string; label: string; options: readonly {value: string; label: string}[]}>;
export type SettingsTableAction = Readonly<{id: string; label: string; destructive?: boolean; onAction: (ids: readonly string[], opener: HTMLElement) => void}>;
export type SettingsTableHandle = {focusRow: (id: string) => void};
export type SettingsTableProps = Readonly<{
  label: string; rows: readonly SettingsTableRow[]; columns: readonly SettingsTableColumn[]; query: TableQuery; onQueryChange: (query: TableQuery) => void;
  filters?: readonly SettingsTableFilter[]; searchLabel: string; countLabel?: string; emptyLabel: string; loading?: boolean; height?: number;
  matchedCount?: number; offset?: number; hasMore?: boolean; onLoadMore?: () => void;
  selected?: ReadonlySet<string>; onSelectionChange?: (selected: ReadonlySet<string>) => void; bulkActions?: readonly SettingsTableAction[];
  rowAction?: Readonly<{label: string; onAction: (row: SettingsTableRow, opener: HTMLElement) => void}>;
  /** One narrow, optional control for the row itself; drawn beside the row action. */
  rowActions?: (row: SettingsTableRow) => ReactNode;
  actions?: ReactNode; ref?: Ref<SettingsTableHandle>;
}>;

function Cell({value, kind}: {value?: SettingsTableCell; kind?: SettingsTableColumn['kind']}) {
  const cell = typeof value === 'string' ? {title: value} : value;
  if (!cell) return null;
  return <div className={styles.cell} data-kind={kind} title={[cell.title, cell.meta].filter(Boolean).join('\n')}>
    {kind === 'main' && (cell.artwork ? <Artwork source={cell.artwork} alt="" className={styles.poster} /> : cell.image ? <img src={cell.image} alt="" className={styles.poster} loading="lazy" /> : cell.avatar ? <span className={styles.avatar} style={cell.avatarKey !== undefined ? accountAvatarStyle(cell.avatarKey) : undefined} data-gradient={cell.avatarKey !== undefined || undefined} aria-hidden="true">{cell.avatar}</span> : null)}
    <span className={kind === 'badge' ? styles.badge : styles.cellText} data-tone={cell.tone}><b>{cell.title}</b>{cell.meta && <small>{cell.meta}</small>}</span>
  </div>;
}

/** Real paged data in, bounded DOM out. It never sorts a partial server page locally. */
export function SettingsTable({label, rows, columns, query, onQueryChange, filters = [], searchLabel, countLabel, emptyLabel, loading = false, height = 560,
  matchedCount, offset = 0, hasMore, onLoadMore, selected, onSelectionChange, bulkActions = [], rowAction, rowActions, actions, ref}: SettingsTableProps) {
  const id = useId(), root = useRef<HTMLDivElement>(null), search = useRef<HTMLInputElement>(null), all = useRef<HTMLInputElement>(null);
  const [viewport, setViewport] = useState({top: 0, height}), [focused, setFocused] = useState<string | undefined>();
  const pending = useRef<{id: string; slot: number} | undefined>(undefined), before = useRef(rows.map(row => row.id));
  const restoreSearch = useRef(false), autoMore = useRef<string | undefined>(undefined);
  const range = settingsTableWindow(rows.length, viewport.top, viewport.height), selectable = !!onSelectionChange;
  const ids = useMemo(() => rows.map(row => row.id), [rows]), selectedCount = selected?.size ?? 0;
  const selectableIds = useMemo(() => rows.filter(row => row.selectable !== false).map(row => row.id), [rows]);
  const allChecked = selectableIds.length > 0 && selectableIds.every(id => selected?.has(id)), anyChecked = selectableIds.some(id => selected?.has(id));
  const filtered = !!query.search || Object.values(query.filters).some(value => value !== 'all');
  const hasActions = Boolean(rowAction || rowActions);
  const columnsCount = columns.length + Number(selectable) + Number(hasActions);
  const changeQuery = (value: TableQuery) => {
    onSelectionChange?.(new Set());
    if (root.current) root.current.scrollTop = 0;
    setViewport(old => ({...old, top: 0})); onQueryChange(value);
  };
  const focusRow = useCallback((rowId: string, slot = 0) => {
    const element = root.current, index = rows.findIndex(row => row.id === rowId);
    if (!element || index < 0) { search.current?.focus(); return; }
    pending.current = {id: rowId, slot};
    const top = index * settingsTableRowHeight, bottom = top + settingsTableRowHeight + settingsTableHeaderHeight;
    if (top < element.scrollTop) element.scrollTop = top;
    else if (bottom > element.scrollTop + element.clientHeight) element.scrollTop = bottom - element.clientHeight;
    setViewport({top: element.scrollTop, height: element.clientHeight});
  }, [rows]);
  useImperativeHandle(ref, () => ({focusRow}), [focusRow]);
  useEffect(() => {
    const element = root.current; if (!element) return;
    const observer = new ResizeObserver(() => setViewport({top: element.scrollTop, height: element.clientHeight}));
    observer.observe(element); return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => { if (all.current) all.current.indeterminate = anyChecked && !allChecked; }, [allChecked, anyChecked]);
  useLayoutEffect(() => {
    const denied = new Set(rows.filter(row => row.selectable === false).map(row => row.id));
    if (selected && [...selected].some(id => denied.has(id))) onSelectionChange?.(new Set([...selected].filter(id => !denied.has(id))));
  }, [rows, selected, onSelectionChange]);
  useLayoutEffect(() => {if (!selectedCount && restoreSearch.current) {search.current?.focus(); restoreSearch.current = false;}}, [selectedCount]);
  useLayoutEffect(() => {
    if (focused && !ids.includes(focused) && (document.activeElement === document.body || root.current?.contains(document.activeElement))) {
      const next = tableFocusFallback(before.current, ids, focused);
      if (next) focusRow(next); else search.current?.focus();
    }
    before.current = ids;
  }, [rows, ids, focused, focusRow]);
  useLayoutEffect(() => {
    const target = pending.current; if (!target) return;
    const row = [...(root.current?.querySelectorAll<HTMLElement>('[data-table-row]') ?? [])].find(element => element.dataset.tableRow === target.id);
    const stops = row?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled)');
    if (stops?.length) { stops[Math.min(target.slot, stops.length - 1)].focus({preventScroll: true}); pending.current = undefined; }
  }, [viewport, rows]);
  useEffect(() => {
    const key = JSON.stringify([offset, rows.length, rows.at(-1)?.id, query]);
    if (hasMore && !loading && range.last >= rows.length - 8 && autoMore.current !== key) {autoMore.current = key; onLoadMore?.();}
  }, [range.last, rows, offset, query, hasMore, loading, onLoadMore]);
  const rendered = rows.slice(range.first, range.last + 1);
  return <section className={styles.tableSection} aria-label={label}>
    <div className={styles.tableToolbar}>
      {selectedCount ? <><span role="status" className={styles.selectionCount}>{t('next.settings.selected', {count: selectedCount})}</span>{bulkActions.map(action => <Button size="xs" key={action.id} className={action.destructive ? styles.danger : undefined} onClick={event => action.onAction([...(selected ?? [])], event.currentTarget)}>{action.label}</Button>)}<Button size="xs" variant="text" onClick={() => {restoreSearch.current = true; onSelectionChange?.(new Set());}}>{t('next.settings.clear')}</Button></> : <>
        <TextField ref={search} containerClassName={styles.tableSearch} type="search" variant="search" value={query.search} placeholder={searchLabel} aria-label={searchLabel} onChange={event => changeQuery({...query, search: event.currentTarget.value})}
          trailing={query.search && <button className={styles.clearSearch} type="button" aria-label={t('next.shell.clearSearch')} onClick={() => {changeQuery({...query, search: ''}); search.current?.focus();}}><Icon name="i-x" size={14} /></button>} />
        {filters.map(filter => <Select key={filter.id} className={[styles.tableFilter, query.filters[filter.id] && query.filters[filter.id] !== 'all' ? styles.tableFilterActive : undefined].filter(Boolean).join(' ')} aria-label={filter.label} value={query.filters[filter.id] ?? 'all'} selectedLabel={!query.filters[filter.id] || query.filters[filter.id] === 'all' ? filter.label : undefined} options={filter.options} onValueChange={value => changeQuery({...query, filters: {...query.filters, [filter.id]: value}})} />)}
        {countLabel && <span className={styles.tableCount} role="status">{countLabel}</span>}{actions}
      </>}
    </div>
    <div ref={root} className={styles.tableScroll} tabIndex={-1} style={{maxHeight: height} as CSSProperties} aria-busy={loading}
      onScroll={event => setViewport({top: event.currentTarget.scrollTop, height: event.currentTarget.clientHeight})}
      onFocusCapture={event => { const row = (event.target as HTMLElement).closest<HTMLElement>('[data-table-row]'); if (row) setFocused(row.dataset.tableRow); }}
      onKeyDown={event => {
        const row = (event.target as HTMLElement).closest<HTMLElement>('[data-table-row]'); if (!row) return;
        const stops = [...row.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled)')], slot = stops.indexOf(event.target as HTMLElement), index = ids.indexOf(row.dataset.tableRow!);
        if (event.altKey || event.metaKey || event.ctrlKey || slot < 0) return;
        const delta = event.key === 'ArrowDown' ? 1 : event.key === 'ArrowUp' ? -1 : 0;
        if (delta && rows[index + delta]) { event.preventDefault(); focusRow(rows[index + delta].id, slot); }
        // Keep natural Tab order across the boundary between mounted windows.
        if (event.key === 'Tab') {
          const direction = event.shiftKey ? -1 : 1, atEdge = event.shiftKey ? slot === 0 : slot === stops.length - 1;
          const next = index + direction;
          if (atEdge && rows[next] && (next < range.first || next > range.last)) { event.preventDefault(); focusRow(rows[next].id, event.shiftKey ? stops.length - 1 : 0); }
        }
      }}>
      <table className={styles.table} aria-label={label} aria-rowcount={matchedCount === undefined ? undefined : matchedCount + 1} aria-colcount={columnsCount}>
        <colgroup>{selectable && <col className={styles.checkboxColumn} />}{columns.map(column => <col key={column.id} style={{width: column.width}} />)}{hasActions && <col className={rowActions ? `${styles.actionColumn} ${styles.actionColumnWide}` : styles.actionColumn} />}</colgroup>
        <thead><tr>{selectable && <th scope="col"><input ref={all} type="checkbox" checked={allChecked} disabled={!selectableIds.length} aria-label={t('next.settings.selectLoaded')} onChange={event => onSelectionChange?.(selectTableRows(selected ?? new Set(), selectableIds, event.currentTarget.checked))} /></th>}
          {columns.map(column => <th key={column.id} scope="col" aria-sort={query.sort.by === column.id ? query.sort.direction === 'asc' ? 'ascending' : 'descending' : undefined} data-kind={column.kind}>
            {column.sortable ? <button type="button" onClick={() => changeQuery({...query, sort: nextTableSort(query.sort, column.id)})}>{column.label}<Icon name={query.sort.by === column.id && query.sort.direction === 'desc' ? 'i-arrow-down' : 'i-arrow-up'} size={14} className={query.sort.by === column.id ? undefined : styles.sortIdle} /></button> : column.label}
          </th>)}{hasActions && <th scope="col"><span className={styles.srOnly}>{t('next.settings.actions')}</span></th>}</tr></thead>
        <tbody>{range.before > 0 && <tr aria-hidden="true"><td colSpan={columnsCount} style={{height: range.before, padding: 0}} /></tr>}
          {rendered.map((row, index) => <tr key={row.id} data-table-row={row.id} aria-rowindex={offset + range.first + index + 2} data-selected={selected?.has(row.id) || undefined}
            onClick={event => {if (!(event.target as HTMLElement).closest('button,input,a')) rowAction?.onAction(row, event.currentTarget.querySelector<HTMLElement>('[data-table-action]') ?? event.currentTarget);}}>
            {selectable && <td><input type="checkbox" checked={row.selectable !== false && (selected?.has(row.id) ?? false)} disabled={row.selectable === false} aria-label={t('next.settings.selectRow', {name: row.label})} onChange={event => {if (row.selectable !== false) onSelectionChange?.(selectTableRows(selected ?? new Set(), [row.id], event.currentTarget.checked));}} /></td>}
            {columns.map(column => <td key={column.id} data-kind={column.kind}><Cell value={row.cells[column.id]} kind={column.kind} /></td>)}
            {hasActions && <td className={styles.actionCell}>{rowActions?.(row)}{rowAction && <Button size="xs" data-table-action aria-label={t('next.settings.rowAction', {action: rowAction.label, name: row.label})} onClick={event => rowAction.onAction(row, event.currentTarget)}>{rowAction.label}</Button>}</td>}
          </tr>)}
          {range.after > 0 && <tr aria-hidden="true"><td colSpan={columnsCount} style={{height: range.after, padding: 0}} /></tr>}
          {!rows.length && !loading && <tr><td className={styles.tableEmpty} colSpan={columnsCount}>{emptyLabel}{filtered && <Button size="xs" variant="text" onClick={() => changeQuery({...query, search: '', filters: Object.fromEntries(filters.map(filter => [filter.id, 'all']))})}>{t('next.settings.clearFilters')}</Button>}</td></tr>}
        </tbody>
      </table>
      {loading && <p className={styles.loading} role="status">{t('next.settings.loading')}</p>}
    </div>
  </section>;
}
