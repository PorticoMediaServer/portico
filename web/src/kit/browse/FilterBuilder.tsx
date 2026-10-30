import {useId, useRef, useState, type ReactNode, type Ref} from 'react';
import {defaultI18n} from '@i18n';
import type {BrowseFieldCapability} from '@core/browse';
import type {BrowseToolbarModel} from '@core/presentation/kit-model';
import {Button, Segmented, TextField} from '../controls';
import {Icon} from '../Icon';
import {Popover} from '../overlays';
import type {BrowseLabels, BrowsePlatform, FacetSelection, FacetValue, FilterFieldChange, FilterFieldState, FilterMatch} from './types';
import styles from './browse.module.css';
import {nextFacetSelection} from './model';

const t = defaultI18n.t;
type ToolbarGroupId = BrowseToolbarModel['groups'][number]['id'];
export type FieldTileProps = Readonly<{label: string; summary?: ReactNode; active?: boolean; expanded?: boolean; forward?: boolean; controls?: string; onClick: () => void; className?: string}>;
export function FieldTile({label, summary, active = false, expanded = false, forward = false, controls, onClick, className, tileRef}: FieldTileProps & {tileRef?: Ref<HTMLButtonElement>}) {
  return <button type="button" ref={tileRef} className={[styles.fieldTile, className].filter(Boolean).join(' ')} data-active={active || undefined} aria-expanded={expanded} aria-controls={controls} onClick={onClick}>
    <span className={styles.fieldLabel}>{label}</span><span className={styles.fieldSummary}>{summary ?? t('next.browse.any')}</span><Icon name={forward ? 'i-right' : expanded ? 'i-up' : 'i-down'} size={18}/>
  </button>;
}
export type FacetChipsProps = Readonly<{
  values: readonly FacetValue[]; included: readonly string[]; excluded: readonly string[];
  onValueChange: (value: string, selection: FacetSelection) => void; cycleExclusion?: boolean; trailing?: ReactNode;
}>;
export function FacetChips({values, included, excluded, onValueChange, cycleExclusion = false, trailing}: FacetChipsProps) {
  return <div className={styles.facetChips}>{values.map(value => {
    const inside = included.includes(value.value), outside = excluded.includes(value.value);
    const next = nextFacetSelection(inside, outside, cycleExclusion);
    return <button type="button" key={value.value} role="checkbox" aria-checked={outside ? 'mixed' : inside} data-selected={inside || undefined}
      className={[styles.facetChip, outside && styles.excludedFacet].filter(Boolean).join(' ')}
      aria-label={t(next === 'exclude' ? 'next.browse.exclude' : next === 'clear' ? 'next.browse.clearValue' : 'next.browse.include', {value: value.label})}
      onClick={() => onValueChange(value.value, next)}>{inside && <Icon name="i-check" size={16}/>}<span>{value.label}</span>{value.count !== undefined && <small>{value.countIsLowerBound ? t('next.browse.countAtLeast', {count: value.count}) : defaultI18n.number(value.count)}</small>}</button>;
  })}{trailing}</div>;
}
export type MatchModeProps = Readonly<{value: FilterMatch; allowAll: boolean; allowNone?: boolean; onValueChange: (value: FilterMatch) => void}>;
export function MatchMode({value, allowAll, allowNone = true, onValueChange}: MatchModeProps) {
  const items = [{value: 'any', label: t('next.browse.anyOf')}, ...(allowAll ? [{value: 'all', label: t('next.browse.allOf')}] : []), ...(allowNone ? [{value: 'none', label: t('next.browse.noneOf')}] : [])];
  return <Segmented variant="browse" value={value} aria-label={t('next.browse.matchMode')} items={items}
    onValueChange={next => { if (next === 'any' || next === 'none' && allowNone || next === 'all' && allowAll) onValueChange(next); }}/>;
}
function active(state: FilterFieldState): boolean {
  if (state.kind === 'values') return state.included.length + state.excluded.length > 0;
  if (state.kind === 'text') return state.value.length > 0;
  return state.value !== undefined;
}
export type FilterBuilderProps = Readonly<{
  model: Pick<BrowseToolbarModel, 'groups'>; labels: Pick<BrowseLabels, 'field'>; platform?: BrowsePlatform;
  fields: Readonly<Record<string, FilterFieldState>>; openFields: readonly string[];
  onFieldOpenChange: (field: string, open: boolean) => void; onFieldChange: (field: string, change: FilterFieldChange) => void;
  onSearchChange: (field: string, query: string) => void; onShowAll?: (field: string) => void; onRetry?: (field: string) => void;
  visibleFieldCount?: Partial<Readonly<Record<ToolbarGroupId, number>>>; moreSummary?: Partial<Readonly<Record<ToolbarGroupId, string>>>;
  note?: ReactNode; className?: string;
}>;
export function FilterBuilder({model, labels, platform = 'desktop', fields, openFields, onFieldOpenChange, onFieldChange, onSearchChange, onShowAll, onRetry,
  visibleFieldCount, moreSummary, note, className}: FilterBuilderProps) {
  const id = useId(), [expandedGroups, setExpandedGroups] = useState<readonly ToolbarGroupId[]>([]);
  const renderField = (field: BrowseFieldCapability) => {
    const state = fields[field.id];
    if (!state) throw new Error(`Missing controlled filter field: ${field.id}`);
    const open = openFields.includes(field.id), label = labels.field(field), contentId = `${id}-${field.id}`;
    const change = (value: FilterFieldChange) => onFieldChange(field.id, value);
    if (platform === 'phone' && state.kind === 'toggle') return <div className={styles.phoneToggleField} key={field.id}><span>{label}</span>
      <Segmented variant="phone" aria-label={label} value={state.value === undefined ? 'any' : state.value ? 'yes' : 'no'}
        items={[{value: 'any', label: t('next.browse.any')}, {value: 'yes', label: t('next.browse.yes')}, {value: 'no', label: t('next.browse.no')}]}
        onValueChange={value => change({kind: 'toggle', value: value === 'any' ? undefined : value === 'yes'})}/></div>;
    const body = <FilterFieldBody field={field} state={state} label={label} platform={platform} contentId={contentId} onFieldOpenChange={onFieldOpenChange}
      onFieldChange={onFieldChange} onSearchChange={onSearchChange} onShowAll={onShowAll} onRetry={onRetry}/>;
    // H10: on desktop a field opens as a menu-like popover anchored to its tile, so the grid never moves.
    if (platform === 'desktop') return <DesktopField key={field.id} label={label} summary={state.summary} active={active(state)} open={open} contentId={contentId}
      onOpenChange={value => onFieldOpenChange(field.id, value)}>{body}</DesktopField>;
    return <div className={open ? styles.expandedField : styles.collapsedField} key={field.id} data-active={active(state) || undefined}>
      {open ? body : <FieldTile label={label} summary={state.summary} active={active(state)} expanded={false} controls={contentId} onClick={() => onFieldOpenChange(field.id, true)}/>}
    </div>;
  };
  return <div className={[styles.filterBuilder, className].filter(Boolean).join(' ')} data-platform={platform}>
    {model.groups.map(group => {
      const count = group.fields.filter(field => fields[field.id] && active(fields[field.id]!)).length;
      const limit = visibleFieldCount?.[group.id] ?? (platform === 'desktop' ? group.id === 'title' ? 7 : group.id === 'file' ? 5 : Infinity : Infinity);
      const more = group.fields.slice(limit), expanded = expandedGroups.includes(group.id), visible = expanded ? group.fields : group.fields.slice(0, limit);
      return <section key={group.id} className={styles.filterGroup} data-group={group.id} aria-labelledby={`${id}-${group.id}-heading`}>
        <h3 id={`${id}-${group.id}-heading`}>{t(`filter.group.${group.id}`)}{platform === 'desktop' && count > 0 && <span>{t('next.browse.activeCount', {count})}</span>}</h3>
        <div className={styles.fieldGrid}>{visible.map(renderField)}
          {more.length > 0 && <button type="button" className={styles.moreFields} aria-expanded={expanded} onClick={() => setExpandedGroups(current => expanded ? current.filter(key => key !== group.id) : [...current, group.id])}>
            <span>{t('next.browse.more')}</span><small>{moreSummary?.[group.id] ?? defaultI18n.list(more.map(labels.field))}</small><Icon name={expanded ? 'i-up' : 'i-down'} size={18}/>
          </button>}
        </div>
      </section>;
    })}{note && <div className={styles.filterNote}>{note}</div>}
  </div>;
}


/** One field's heading and options: the desktop popover and the inline phone/TV field draw the same body. */
export function FilterFieldBody({field, state, label, platform = 'desktop', contentId, onFieldOpenChange, onFieldChange, onSearchChange, onShowAll, onRetry}: Readonly<{
  field: BrowseFieldCapability; state: FilterFieldState; label: string; platform?: BrowsePlatform; contentId: string;
  onFieldOpenChange: FilterBuilderProps['onFieldOpenChange']; onFieldChange: FilterBuilderProps['onFieldChange']; onSearchChange: FilterBuilderProps['onSearchChange'];
  onShowAll?: FilterBuilderProps['onShowAll']; onRetry?: FilterBuilderProps['onRetry'];}>) {
  const canExclude = field.operators.includes('not-in') || field.operators.includes('not-equals');
  const change = (value: FilterFieldChange) => onFieldChange(field.id, value);
  const heading = <div className={styles.expandedHeading}><button type="button" className={styles.expandedName} aria-expanded aria-controls={contentId} onClick={() => onFieldOpenChange(field.id, false)}>
      {label}{state.summary && <span>{state.summary}</span>}<Icon name={platform === 'tv' ? 'i-down' : 'i-up'} size={16}/></button>
      {platform === 'desktop' && active(state) && <Button variant="text" size="xs" onClick={() => change({kind: 'clear'})}>{t('next.browse.clear')}</Button>}</div>;
  const content = <div className={styles.fieldContent} id={contentId}>
      {state.kind === 'values' && <>
        {platform === 'desktop' && <MatchMode value={state.match} allowAll={field.operators.includes('contains-all')} allowNone={canExclude} onValueChange={value => change({kind: 'match', value})}/>}
        {(state.total === undefined || state.total > 10 || state.query) && platform !== 'tv' && <TextField variant="search" containerClassName={styles.facetFind} leadingIcon="i-search" value={state.query}
          aria-label={t('next.browse.find', {field: label.toLocaleLowerCase()})} placeholder={t('next.browse.find', {field: label.toLocaleLowerCase()})}
          onChange={event => onSearchChange(field.id, event.target.value)}/>}
        {state.status === 'loading' && <div role="status" className={styles.fieldNotice}>{t('next.browse.loadingValues')}</div>}
        {state.status === 'error' && <div className={styles.fieldNotice} role="alert">{state.error}{onRetry && <Button variant="text" size="small" onClick={() => onRetry(field.id)}>{t('next.browse.retry')}</Button>}</div>}
        {(state.status !== 'error' || state.values.length > 0) &&
          <>{platform === 'tv' && state.hint && <p className={styles.facetHelp}>{state.hint}</p>}<FacetChips values={state.values} included={state.included} excluded={state.excluded} cycleExclusion={platform === 'phone' && canExclude}
            trailing={platform === 'phone' && state.total !== undefined && state.total > state.values.length && onShowAll ? <Button variant="text" size="xs" className={styles.inlineMore} onClick={() => onShowAll(field.id)}>{state.moreLabel ?? t('next.browse.showAll', {count: state.total, field: label.toLocaleLowerCase()})}</Button> : undefined}
            onValueChange={(value, selection) => change({kind: 'facet', value, selection: state.match === 'none' && selection === 'include' ? 'exclude' : selection})}/>
            {state.status !== 'loading' && state.values.length === 0 && <span className={styles.fieldNotice}>{t('next.browse.noValues')}</span>}
            {platform !== 'phone' && state.total !== undefined && state.total > state.values.length && onShowAll && <Button variant="text" size="xs" className={styles.showAll} onClick={() => onShowAll(field.id)}>
              {state.moreLabel ?? t('next.browse.showAll', {count: state.total, field: label.toLocaleLowerCase()})}<Icon name="i-down" size={14}/></Button>}</>}
        {platform === 'phone' && canExclude && <p className={styles.facetHelp}>{t('next.browse.facetHelp')}</p>}
      </>}
      {state.kind === 'toggle' && <Segmented variant={platform === 'phone' ? 'phone' : 'browse'} value={state.value === undefined ? 'any' : state.value ? 'yes' : 'no'}
        items={[{value: 'any', label: t('next.browse.any')}, {value: 'yes', label: t('next.browse.yes')}, {value: 'no', label: t('next.browse.no')}]}
        onValueChange={value => change({kind: 'toggle', value: value === 'any' ? undefined : value === 'yes'})}/>}
      {(state.kind === 'choice' || state.kind === 'range') && <div className={styles.facetChips}>
        <button type="button" className={styles.facetChip} data-selected={state.value === undefined || undefined} aria-pressed={state.value === undefined} onClick={() => change({kind: 'choice', value: undefined})}>{t('next.browse.any')}</button>
        {state.options.map(option => <button type="button" className={styles.facetChip} data-selected={state.value === option.value || undefined} aria-pressed={state.value === option.value} key={option.value}
          onClick={() => change({kind: 'choice', value: option.value === state.value ? undefined : option.value})}>{option.label}</button>)}
      </div>}
      {state.kind === 'text' && <TextField variant="field" value={state.value} label={label} placeholder={state.placeholder} onChange={event => change({kind: 'text', value: event.target.value})}/>}
    </div>;
  return <>{heading}{content}</>;
}
function DesktopField({label, summary, active, open, contentId, onOpenChange, children}: {label: string; summary?: ReactNode; active: boolean; open: boolean; contentId: string; onOpenChange: (open: boolean) => void; children: ReactNode}) {
  const tile = useRef<HTMLButtonElement>(null), [width, setWidth] = useState(360);
  return <div className={styles.collapsedField} data-active={active || undefined} data-open={open || undefined}>
    <FieldTile tileRef={tile} label={label} summary={summary} active={active} expanded={open} controls={open ? contentId : undefined}
      onClick={() => { setWidth(Math.max(360, Math.round(tile.current?.getBoundingClientRect().width ?? 0))); onOpenChange(!open); }}/>
    <Popover open={open} onOpenChange={value => onOpenChange(value)} anchorRef={tile} title={label} placement="bottom-start" sideOffset={6} width={width} className={styles.fieldPopover}>
      {children}
    </Popover>
  </div>;
}
