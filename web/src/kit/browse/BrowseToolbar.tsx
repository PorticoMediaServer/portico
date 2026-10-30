import {useRef, useState, type ButtonHTMLAttributes, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {BrowseSortCapability, BrowseSortSelection} from '@core/browse';
import {sortDirectionKind} from '@core/presentation/browse-toolbar';
import type {BrowseToolbarModel} from '@core/presentation/kit-model';
import {Button, Chip, Segmented} from '../controls';
import {Menu, MenuRow} from '../overlays';
import {Icon, type IconName} from '../Icon';
import type {AppliedChip, BrowseLabels, BrowsePlatform, BrowseView, CardSizeControl, SaveViewControl} from './types';
import styles from './browse.module.css';
import {nextBrowseSort} from './model';
import {useAcceptedArrival, useAppearanceFade, type AcceptedAnswer} from './use-arrival';

const t = defaultI18n.t;
export function sortDirectionLabel(field: string, direction: 'asc' | 'desc'): string {
  return t(`next.browse.${direction}.${sortDirectionKind(field)}`);
}
export type FiltersButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & Readonly<{count?: number; open?: boolean}>;
export function FiltersButton({count = 0, open = false, className, ...props}: FiltersButtonProps) {
  return <Button {...props} variant="glass" size="small" icon="i-filter" className={[styles.filtersButton, className].filter(Boolean).join(' ')} aria-expanded={open}>
    <span className={styles.filtersLabel}>{t('next.browse.filters')}{count > 0 && <span className={styles.filterBadge} aria-label={t('next.browse.filterCount', {count})}>{defaultI18n.number(count)}</span>}</span>
  </Button>;
}
export type QuickToggleProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'onChange'> & Readonly<{
  selected: boolean; onCheckedChange: (checked: boolean) => void; icon?: IconName; count?: number;
}>;
export function QuickToggle({selected, onCheckedChange, icon, count, children, className, ...props}: QuickToggleProps) {
  return <Chip {...props} variant="quick" selected={selected} icon={icon} count={count} onClick={event => { props.onClick?.(event); if (!event.defaultPrevented) onCheckedChange(!selected); }} className={[styles.quickToggle, className].filter(Boolean).join(' ')}>{children}</Chip>;
}
export type SortMenuProps = Readonly<{
  sorts: readonly BrowseSortCapability[]; value: BrowseToolbarModel['sort']; onValueChange: (value: BrowseSortSelection) => void;
  label: BrowseLabels['sort']; open?: boolean; onOpenChange?: (open: boolean) => void; onReshuffle?: () => void;
  /** A surface that names its own order draws no direction word beside it. */
  showOrder?: boolean;
  /** `saved` is the Saved toolbar's drawn chip: a 15px inset and a 4px rule before the order word. */
  variant?: 'default' | 'saved';
}>;
export function SortMenu({sorts, value, onValueChange, label, open: controlledOpen, onOpenChange, onReshuffle, showOrder: allowOrder = true, variant = 'default'}: SortMenuProps) {
  const triggerRef = useRef<HTMLButtonElement>(null);
  const [localOpen, setLocalOpen] = useState(false), open = controlledOpen ?? localOpen;
  const setOpen = (next: boolean) => { setLocalOpen(next); onOpenChange?.(next); };
  const selected = sorts.find(sort => sort.id === value.field), showOrder = allowOrder && value.flippable && selected;
  const pick = (sort: BrowseSortCapability) => {
    if (sort.id === value.field && value.reshufflable) {
      if (onReshuffle) onReshuffle(); else onValueChange({field: value.field, direction: value.direction, seed: (value.seed ?? 0) + 1});
      setOpen(false); return;
    }
    const next = nextBrowseSort(sorts, value, sort.id);
    if (next) onValueChange(next);
    setOpen(false);
  };
  return <Menu presentation="anchored" returnFocusRef={triggerRef} open={open} onOpenChange={setOpen} title={t('next.browse.sortBy')} accessibilityLabel={t('next.browse.sortBy')} placement="bottom-end" width={300} className={styles.sortMenu}
    trigger={<Chip ref={triggerRef} variant="default" icon="i-sort" iconSize={18} trailingIcon="i-down" trailingSize={16} className={styles.sortTrigger} data-saved={variant === 'saved' || undefined} aria-label={[selected ? label(selected) : t('next.browse.sort'), value.flippable ? sortDirectionLabel(value.field, value.direction) : undefined].filter(Boolean).join(', ')} aria-expanded={open}><span className={styles.sortLabel}>{selected ? label(selected) : t('next.browse.sort')}{showOrder && <>{variant === 'saved' && <span className={styles.orderSeparator} aria-hidden="true" />}<span className={styles.direction}>{sortDirectionLabel(value.field, value.direction)}</span></>}</span></Chip>}>
    <div className={styles.menuHeading}>{t('next.browse.sortBy')}</div>
    {sorts.map(sort => <MenuRow key={sort.id} role="menuitemradio" aria-checked={sort.id === value.field} data-menu-item="" tabIndex={-1}
      selected={sort.id === value.field} onClick={() => pick(sort)} description={sort.expensive ? t('next.browse.expensive') : undefined}>{label(sort)}</MenuRow>)}
    {showOrder && <div className={styles.sortOrder}><div className={styles.menuHeading}>{t('next.browse.order')}</div>
      <Segmented variant="browse" value={value.direction} aria-label={t('next.browse.order')} onValueChange={direction => { if ((direction === 'asc' || direction === 'desc') && selected.directions.includes(direction)) onValueChange({field: value.field, direction}); }}
        items={[...selected.directions].sort(direction => direction === selected.defaultDirection ? -1 : 1).map(direction => ({value: direction, label: sortDirectionLabel(value.field, direction)}))}/></div>}
  </Menu>;
}
export function ViewToggle({value, onValueChange}: BrowseView) {
  return <div className={styles.viewToggle} role="group" aria-label={t('next.browse.view')}>
    <Button variant="icon" size="small" icon="i-grid" iconOnly aria-label={t('next.browse.grid')} aria-pressed={value === 'grid'} onClick={() => onValueChange('grid')}/>
    <Button variant="icon" size="small" icon="i-list" iconOnly aria-label={t('next.browse.list')} aria-pressed={value === 'list'} onClick={() => onValueChange('list')}/>
  </div>;
}
export function CardSizeButton({value, options, onValueChange}: CardSizeControl) {
  const [open, setOpen] = useState(false);
  return <Menu presentation="anchored" open={open} onOpenChange={setOpen} title={t('next.browse.cardSize')} accessibilityLabel={t('next.browse.cardSize')} placement="bottom-end" width={180}
    trigger={<Button variant="icon" size="small" icon="i-size" iconOnly aria-label={t('next.browse.cardSize')}/>}>
    {options.map(option => <MenuRow key={option.value} role="menuitemradio" aria-checked={option.value === value} data-menu-item="" tabIndex={-1}
      selected={option.value === value} onClick={() => {onValueChange(option.value); setOpen(false);}}>{option.label}</MenuRow>)}
  </Menu>;
}
export function SaveViewButton({canSave, onSave}: SaveViewControl) {
  return <Button variant="text" size="small" icon="i-save" className={styles.saveView} disabled={!canSave} onClick={onSave}>{t('next.browse.saveView')}</Button>;
}
export type AppliedFilterChipsProps = Readonly<{items: readonly AppliedChip[]; onRemove: (id: string) => void; onClear: () => void; className?: string}>;
/** One applied chip; motion 3.8 fades the chip in when its own identity appears under the toolbar. */
function AppliedChipButton({chip, onRemove}: {chip: AppliedChip; onRemove: (id: string) => void}) {
  const ref = useRef<HTMLButtonElement>(null), {id, parts} = chip;
  useAppearanceFade(ref);
  return <button ref={ref} type="button" className={styles.appliedChip} aria-label={t('next.browse.removeFilter', {field: parts.field.text})} onClick={() => onRemove(id)}>
    <span className={styles.appliedField}>{parts.field.text}</span>
    {parts.operator !== 'equals' && parts.operator !== 'in' && !(parts.operator === 'contains-any' && parts.values.length === 1) && <span className={styles.appliedOperator}>{parts.operatorPart.text}</span>}
    {parts.values.map(value => <span key={value.id} className={styles.appliedValue}>{value.text}</span>)}<span className={styles.removeIcon}><Icon name="i-x" size={12}/></span>
  </button>;
}
export function AppliedFilterChips({items, onRemove, onClear, className}: AppliedFilterChipsProps) {
  if (!items.length) return null;
  return <div className={[styles.applied, className].filter(Boolean).join(' ')} aria-label={t('next.browse.applied')}>
    {items.map(chip => <AppliedChipButton key={chip.id} chip={chip} onRemove={onRemove}/>)}
    <Button variant="text" size="xs" onClick={onClear}>{t('next.browse.clearAll')}</Button>
  </div>;
}
export type BrowseToolbarProps = Readonly<{
  model: BrowseToolbarModel; labels: BrowseLabels; resultCount: ReactNode; platform?: BrowsePlatform; sticky?: boolean; topInset?: number;
  onQuickChange: (id: string, checked: boolean) => void; onFilters: () => void; filtersOpen?: boolean;
  onSortChange: (value: BrowseSortSelection) => void; onReshuffle?: () => void; view?: BrowseView; cardSize?: CardSizeControl;
  save?: SaveViewControl; applied?: readonly AppliedChip[]; onRemoveFilter?: (id: string) => void; onClearFilters?: () => void;
  onJump?: () => void; selection?: ReactNode; actions?: ReactNode; className?: string;
  /** Actual selection mode; a mounted host may render no pixels while retaining keyboard/dialog ownership. */
  selectionActive?: boolean;
  /** A surface that names its one sort itself (a row's own order reads "Best match"). */
  sortLabel?: string;
  /** The quick filters this surface draws, in the model's order; absent draws all offered. */
  quickIds?: readonly string[];
  /** Presentation only: the collection's own accepted-answer witness and the collection it belongs to. */
  accepted?: AcceptedAnswer; acceptedSource?: unknown;
}>;
/** The drawn count; motion 3.8 cross-fades it when an accepted answer lands, and it stays a plain span otherwise. */
function ResultCount({children, accepted, source, className}: {children: ReactNode; accepted?: AcceptedAnswer; source?: unknown; className?: string}) {
  const ref = useRef<HTMLSpanElement>(null);
  useAcceptedArrival(ref, accepted, source);
  return <span ref={ref} className={className}>{children}</span>;
}
export function BrowseToolbar({model, labels, resultCount, platform = 'desktop', sticky = false, topInset = 76, onQuickChange, onFilters, filtersOpen,
  onSortChange, onReshuffle, view, cardSize, save, applied = [], onRemoveFilter, onClearFilters, onJump, selection, actions, className, sortLabel, quickIds, accepted, acceptedSource, selectionActive}: BrowseToolbarProps) {
  const sortLabels: BrowseLabels = sortLabel === undefined ? labels : {...labels, sort: (value: BrowseSortCapability) => value.id === model.sort.field ? sortLabel : labels.sort(value)};
  const quick = quickIds === undefined ? model.quick : model.quick.filter(item => quickIds.includes(item.id));
  const sort = <SortMenu sorts={model.sorts} value={model.sort} onValueChange={onSortChange} label={sortLabels.sort} onReshuffle={onReshuffle} showOrder={sortLabel === undefined}/>;
  const filters = <FiltersButton count={applied.length} open={filtersOpen} onClick={onFilters}/>;
  // Frame 6 draws a library's sort beside Filters in the chip row; See all names its own
  // order on the count row instead, so a surface that supplies sortLabel keeps that one.
  const sortInFirstRow = platform === 'phone' && sortLabel === undefined;
  return <div className={[styles.toolbar, className].filter(Boolean).join(' ')} data-platform={platform} data-sticky={sticky || undefined} style={{top: topInset}}>
    {selection}{!(selectionActive ?? selection != null) && <><div className={styles.toolbarMain}>
      {platform === 'tv' ? <>{sort}{filters}{onJump && <Chip onClick={onJump}>{t('next.browse.jump')}<Icon name="i-down" size={16}/></Chip>}</> : filters}
      <span className={styles.separator} aria-hidden="true"/>
      {sortInFirstRow && sort}
      {actions}{quick.map(item => <QuickToggle key={item.id} selected={item.active} onCheckedChange={checked => onQuickChange(item.id, checked)}>{item.label?.text ?? labels.quick(item)}</QuickToggle>)}
      {platform !== 'phone' && <div className={styles.toolbarEnd}>{platform === 'desktop' && <ResultCount className={styles.resultCount} accepted={accepted} source={acceptedSource}>{resultCount}</ResultCount>}{platform === 'desktop' && sort}
        {view && <ViewToggle {...view}/>} {cardSize && <CardSizeButton {...cardSize}/>} {save && <SaveViewButton {...save}/>}</div>}
    </div>{platform === 'phone' && <div className={styles.phoneCount}><ResultCount accepted={accepted} source={acceptedSource}>{resultCount}</ResultCount>{save && <SaveViewButton {...save}/>}<span className={styles.phoneView}>{!sortInFirstRow && sort}{view && <ViewToggle {...view}/>}</span></div>}
    {applied.length > 0 && onRemoveFilter && onClearFilters && <div className={platform === 'tv' ? styles.tvAppliedRow : undefined}><AppliedFilterChips items={applied} onRemove={onRemoveFilter} onClear={onClearFilters}/>{platform === 'tv' && <ResultCount className={styles.resultCount} accepted={accepted} source={acceptedSource}>{resultCount}</ResultCount>}</div>}</>}
  </div>;
}
