import {channelFieldLabel, channelFieldMessage, channelValueLabel} from '@core/server-admin/channel-catalogue.ts';
import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import type {Library} from '@core/index.ts';
import type {ChannelApi} from '@core/channel-guide.ts';
import {parseBrowseNode, type BrowseFacetValue, type BrowseOperator, type BrowseScalar} from '@core/browse.ts';
import type {LibraryChannelQuery} from '@core/library-channels.ts';
import {channelConditionKey, channelConditionNode, channelConditionsWithLegacy, isChannelConditionGroup, type ChannelCondition, type ChannelConditionGroup, type ChannelConditions as Conditions} from '@core/server-admin/channel-conditions.ts';
import {Button, RichSelect, TextField} from '../../../../kit/controls';
import {ChannelFieldsSource, type ChannelField} from './channel-fields-source';
import styles from './channels.module.css';
const t = defaultI18n.t;
const words: Record<BrowseOperator, MessageId> = {'equals':'next.kit.filter.equals','not-equals':'next.kit.filter.not','contains':'next.kit.filter.contains','starts-with':'next.kit.filter.startsWith','in':'next.kit.filter.in','not-in':'next.kit.filter.notIn','less-than':'next.kit.filter.lessThan','at-most':'next.kit.filter.atMost','greater-than':'next.kit.filter.greaterThan','at-least':'next.kit.filter.atLeast','within-last-days':'filter.preset.lastDays','between':'next.kit.filter.between','is-present':'next.kit.filter.present','is-missing':'next.kit.filter.missing','contains-any':'next.kit.filter.containsAny','contains-all':'next.kit.filter.containsAll'};
const lists = ['in', 'not-in', 'contains-any', 'contains-all', 'between'];
type Props = Readonly<{api: ChannelApi; libraries: readonly Library[]; query: LibraryChannelQuery; locked: boolean; isCurrent: () => boolean; reportError: (error: unknown) => void; onChange: (next: Conditions, valid: boolean) => boolean}>;
export function ChannelConditions(props: Props) {
  const [source, setSource] = useState<ChannelFieldsSource>();
  const libraryScope=JSON.stringify(props.libraries.map(library=>[library.id,library.kind]));
  const setup=useRef(props);useLayoutEffect(()=>{setup.current=props;});
  useLayoutEffect(() => {const source = new ChannelFieldsSource(props.api, setup.current.libraries, props.isCurrent, props.reportError);setSource(source);return source.dispose;}, [props.api, libraryScope, props.isCurrent, props.reportError]);
  return source ? <ConditionsBody {...props} source={source}/> : null;
}
function valid(tree: Conditions): boolean {
  if (tree.opaque) return true;
  for (const item of tree.items) {
    const rows = isChannelConditionGroup(item) ? item.conditions : [item];
    if (!rows.length || rows.some(row => !row.predicates.length)) return false;
    for (const row of rows) for (const predicate of row.predicates) {
      const values = Array.isArray(predicate.value) ? predicate.value : [predicate.value];
      if (!['is-present','is-missing'].includes(predicate.operator) && values.some(value => value === null || typeof value === 'string' && !value.trim() || typeof value === 'number' && !Number.isFinite(value))) return false;
    }
  }
  try {const node = channelConditionNode(tree);if (node) parseBrowseNode(node);return true;} catch {return false;}
}
function ConditionsBody({source, query, locked, isCurrent, onChange}: Props & {source: ChannelFieldsSource}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const {filter, genres, yearFrom, yearThrough} = query;
  const incoming = useMemo(() => channelConditionsWithLegacy({filter, genres, yearFrom, yearThrough}), [filter, genres, yearFrom, yearThrough]);
  const projection = JSON.stringify(channelConditionNode(incoming));
  const [tree, setTree] = useState(incoming), [authority] = useState(() => ({tree: incoming, projection}));
  useLayoutEffect(() => {
    if (authority.projection === projection || !valid(authority.tree)) return;
    authority.projection = projection;
    // A local edit's accepted echo keeps its row keys and private child state.
    // A different accepted producer tree replaces the old edit receipt at commit.
    if (JSON.stringify(channelConditionNode(authority.tree)) !== projection) {
      authority.tree = incoming;setTree(incoming);
    }
  }, [authority, incoming, projection]);
  const libraryKey = JSON.stringify(query.libraryIds);
  useEffect(() => {void source.select(JSON.parse(libraryKey) as string[]);}, [source, libraryKey]);
  const fields = state.fields.filter(entry => channelFieldMessage(entry.field) !== undefined);
  const update = (next: Conditions) => {
    if (locked || !isCurrent() || authority.tree !== tree || authority.projection !== projection) return;
    if (!onChange(next, valid(next))) return;
    authority.tree = next;setTree(next);
  };
  const add = (group?: ChannelConditionGroup) => {
    const entry = fields[0];if (!entry) return;
    const row: ChannelCondition = {key: channelConditionKey(), field: entry.field.id, predicates: [], not: false};
    update({...tree, items: group ? tree.items.map(item => item === group ? {...group, conditions: [...group.conditions, row]} : item) : [...tree.items, row]});
  };
  const replace = (before: ChannelCondition, next: ChannelCondition | undefined, group?: ChannelConditionGroup) => {
    const change = (rows: readonly ChannelCondition[]) => rows.flatMap(row => row !== before ? [row] : next ? [next] : []);
    update({...tree, items: group ? tree.items.map(item => item === group ? {...group, conditions: change(group.conditions)} : item) : tree.items.flatMap(item => item !== before ? [item] : next ? [next] : [])});
  };
  const mode = (value: 'all'|'any', change: (value: 'all'|'any') => void) => <RichSelect aria-label={t('builder.conditions')} value={value} options={(['all','any'] as const).map(value => ({value, label: t(value === 'all' ? 'builder.matchAll' : 'builder.matchAny')}))} onValueChange={value => {if (value === 'all' || value === 'any') change(value);}} disabled={locked}/>;
  const row = (condition: ChannelCondition, group?: ChannelConditionGroup) => <div className={styles.condition} key={condition.key}>
    <RichSelect aria-label={t('builder.addCondition')} value={condition.field} disabled={locked} options={fields.map(entry => ({value: entry.field.id, label: channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')}))} onValueChange={field => {if (fields.some(entry => entry.field.id === field)) replace(condition, {...condition, field, predicates: []}, group);}}/>
    {fields.find(entry => entry.field.id === condition.field) ? <PredicateControl source={source} entry={fields.find(entry => entry.field.id === condition.field)!} condition={condition} locked={locked} onChange={predicates => replace(condition, {...condition, predicates}, group)}/> : <p className={styles.help}>{t('builder.opaqueConditions')}</p>}
    <label className={styles.help}><input type="checkbox" disabled={locked} checked={condition.not} onChange={event => replace(condition, {...condition, not: event.target.checked}, group)}/>{t('builder.exclude')}</label>
    <Button size="small" variant="text" disabled={locked} onClick={() => replace(condition, undefined, group)}>{t('builder.remove')}</Button>
  </div>;
  return <section className={styles.editorSection}><h3>{t('builder.conditions')}</h3>
    {tree.opaque ? <><p className={styles.help}>{t('builder.opaqueConditions')}</p><Button size="small" variant="text" disabled={locked} onClick={() => update({mode:'all',items:[]})}>{t('builder.remove')}</Button></> : <>
      {!!tree.items.length && mode(tree.mode, mode => update({...tree, mode}))}
      {!tree.items.length && <p className={styles.help}>{t('builder.noConditions')}</p>}
      {tree.items.map(item => isChannelConditionGroup(item) ? <div className={styles.conditionGroup} key={item.key}>
        {mode(item.mode, mode => update({...tree, items: tree.items.map(row => row === item ? {...item, mode} : row)}))}
        <label className={styles.help}><input type="checkbox" disabled={locked} checked={item.not} onChange={event => update({...tree, items: tree.items.map(row => row === item ? {...item, not:event.target.checked} : row)})}/>{t('builder.exclude')}</label>
        {item.conditions.map(condition => row(condition, item))}
        <Button size="small" variant="text" disabled={locked || !fields.length || item.conditions.length >= 16} onClick={() => add(item)}>{t('builder.addCondition')}</Button>
        <Button size="small" variant="text" disabled={locked} onClick={() => update({...tree, items:tree.items.filter(row => row !== item)})}>{t('builder.remove')}</Button>
      </div> : row(item))}
      <div className={styles.choices}><Button size="small" variant="glass" disabled={locked || !fields.length || tree.items.length >= 16} onClick={() => add()}>{t('builder.addCondition')}</Button><Button size="small" variant="glass" disabled={locked || tree.items.length >= 16} onClick={() => update({...tree, items:[...tree.items,{key:channelConditionKey(),mode:'any',conditions:[],not:false}]})}>{t('builder.addGroup')}</Button></div>
    </>}
    {!valid(tree) && <p role="alert" className={styles.warning}>{t('builder.error.conditions')}</p>}
    {state.phase === 'loading' && <p role="status" className={styles.help}>{t('next.settings.loading')}</p>}
    {state.phase === 'error' && <Button size="small" variant="text" disabled={locked} onClick={() => {if (isCurrent()) void source.refresh();}}>{t('action.tryAgain')}</Button>}
    <p className={styles.help}>{t('builder.showsNote')}</p>
  </section>;
}
function PredicateControl({source, entry, condition, locked, onChange}: Readonly<{source: ChannelFieldsSource; entry: ChannelField; condition: ChannelCondition; locked: boolean; onChange: (predicates: ChannelCondition['predicates']) => void}>) {
  const first = condition.predicates[0], operator = first?.operator ?? entry.field.operators[0];
  const [values, setValues] = useState<readonly BrowseFacetValue[]>([]), [query, setQuery] = useState('');
  const facet = entry.field.type === 'identity-set' || entry.field.controlHint === 'facet-multi-select';
  useEffect(() => {
    const abort = new AbortController();setValues([]);
    if (!facet) return () => abort.abort();
    const timer = setTimeout(() => {void source.values(entry, query, abort.signal).then(values => {if (!abort.signal.aborted) setValues(values);}, () => {});}, 200);
    return () => {abort.abort();clearTimeout(timer);};
  }, [source, entry, facet, query]);
  if (condition.predicates.length > 1) return <><p className={styles.help}>{t('builder.opaqueConditions')}</p><Button size="small" variant="text" disabled={locked} onClick={() => onChange([])}>{t('builder.remove')}</Button></>;
  const put = (value: BrowseScalar | readonly BrowseScalar[] | null, next = operator) => {if (next && entry.field.operators.includes(next)) onChange([{field:entry.field.id,operator:next,value}]);};
  const scalar = (text: string) => entry.field.type === 'number' || operator === 'within-last-days' ? text.trim() ? Number(text) : NaN : text;
  const selected = Array.isArray(first?.value) ? first.value : first?.value === undefined || first.value === null ? [] : [first.value];
  return <div className={styles.predicate}>
    <RichSelect aria-label={channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')} value={operator ?? ''} disabled={locked} options={entry.field.operators.map(value => ({value,label:t(words[value], {count:'…'})}))} onValueChange={value => {const next=entry.field.operators.find(operator => operator === value);if (!next) return;put(['is-present','is-missing'].includes(next) ? null : lists.includes(next) ? [] : '', next);}}/>
    {!['is-present','is-missing'].includes(operator ?? '') && (facet ? <>
      <TextField label={t('web.filters.find', {field:channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')})} value={query} disabled={locked} maxLength={200} onChange={event => setQuery(event.target.value)}/>
      <div className={styles.choices}>{values.map(value => <label key={value.value}><input type="checkbox" checked={selected.includes(value.value)} disabled={locked} onChange={event => {const next=selected.filter(item => item !== value.value);if (event.target.checked) next.push(value.value);put(lists.includes(operator ?? '') ? next : event.target.checked ? value.value : '');}}/>{value.label}</label>)}</div>
      {!!selected.length && <Button size="small" variant="text" disabled={locked} onClick={() => onChange([])}>{t('builder.remove')}</Button>}
    </> : entry.field.type === 'boolean' ? <RichSelect aria-label={channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')} value={first?.value === true ? 'yes' : first?.value === false ? 'no' : ''} disabled={locked} options={[{value:'yes',label:t('web.filters.yes')},{value:'no',label:t('web.filters.no')}]} onValueChange={value => put(value === 'yes')}/> : entry.field.allowedValues ? lists.includes(operator ?? '') ? <div className={styles.choices}>{entry.field.allowedValues.map(value => <label key={value}><input type="checkbox" checked={selected.includes(value)} disabled={locked} onChange={event => put(event.target.checked ? [...selected.filter(item => item !== value),value] : selected.filter(item => item !== value))}/>{channelValueLabel(value, t)}</label>)}</div> : <RichSelect aria-label={channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')} value={typeof first?.value === 'string' ? first.value : ''} disabled={locked} options={entry.field.allowedValues.map(value => ({value,label:channelValueLabel(value, t)}))} onValueChange={value => {if (entry.field.allowedValues?.includes(value)) put(value);}}/> : <div className={styles.predicate}>{(operator === 'between' ? [0,1] : lists.includes(operator ?? '') ? Array.from({length:Math.max(1,selected.length)},(_,index) => index) : [0]).map(index => <div key={index} className={styles.choices}><TextField aria-label={channelFieldLabel(entry.field, t) ?? t('builder.opaqueConditions')} type={entry.field.type === 'number' || operator === 'within-last-days' ? 'number' : entry.field.type === 'date' ? 'date' : 'text'} value={typeof selected[index] === 'number' && !Number.isFinite(selected[index]) ? '' : String(selected[index] ?? '')} disabled={locked} maxLength={500} onChange={event => {const value=scalar(event.target.value);if (operator === 'between') {const next: BrowseScalar[]=[selected[0] ?? '',selected[1] ?? ''];next[index]=value;put(next);} else if (lists.includes(operator ?? '')) {const next: BrowseScalar[]=selected.length ? [...selected] : [''];next[index]=value;put(next);} else put(value);}}/>{lists.includes(operator ?? '') && operator !== 'between' && <Button size="small" variant="text" disabled={locked} onClick={() => put(selected.filter((_,i) => i !== index))}>{t('builder.remove')}</Button>}</div>)}{lists.includes(operator ?? '') && operator !== 'between' && <Button size="small" variant="text" disabled={locked || selected.length >= 100} onClick={() => put([...selected,''])}>{t('player.opt.add')}</Button>}</div>)}
  </div>;
}
