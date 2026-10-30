import {useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import type {Library} from '@core/index.ts';
import type {ChannelApi} from '@core/channel-guide.ts';
import {Button, TextField} from '../../../../kit/controls';
import {ChannelFieldsSource, type PinTitle} from './channel-fields-source';
import styles from './channels.module.css';

const t = defaultI18n.t;
type Props = Readonly<{api: ChannelApi; libraries: readonly Library[]; libraryIds: readonly string[]; ids: readonly string[]; excluded?: boolean; locked: boolean; isCurrent: () => boolean; reportError: (error: unknown) => void; onChange: (ids: string[]) => void}>;
export function ChannelPins(props: Props) {
  const [source, setSource] = useState<ChannelFieldsSource>();
  const libraryScope=JSON.stringify(props.libraries.map(library=>[library.id,library.kind]));
  const setup=useRef(props);useLayoutEffect(()=>{setup.current=props;});
  useLayoutEffect(() => {
    const source = new ChannelFieldsSource(props.api, setup.current.libraries, props.isCurrent, props.reportError);
    setSource(source);
    return source.dispose;
  }, [props.api, libraryScope, props.isCurrent, props.reportError]);
  return source ? <Pins key={props.excluded ? 'exclude' : 'include'} {...props} source={source}/> : null;
}
function Pins({source, libraryIds, ids, excluded, locked, isCurrent, onChange}: Props & {source: ChannelFieldsSource}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [query, setQuery] = useState(''), [found, setFound] = useState<readonly PinTitle[]>(), [failed, setFailed] = useState(false);
  const [shown, setShown] = useState(20), [, labelsChanged] = useState(0);
  const libraryKey = JSON.stringify(libraryIds), idsKey = JSON.stringify(ids.slice(Math.max(0, shown - 20), shown));
  useEffect(() => {void source.select(JSON.parse(libraryKey) as string[]);}, [source, libraryKey]);
  useEffect(() => {
    const abort = new AbortController();
    if (state.phase === 'ready') void source.labels(JSON.parse(idsKey) as string[], abort.signal).then(() => {if (!abort.signal.aborted && isCurrent()) labelsChanged(value => value + 1);});
    return () => abort.abort();
  }, [source, idsKey, state.phase, isCurrent]);
  useEffect(() => {
    const abort = new AbortController();
    const timer = setTimeout(() => {
      if (state.phase !== 'ready' || query.trim().length < 2 || !isCurrent()) return;
      void source.search(query, abort.signal).then(page => {if (!abort.signal.aborted && isCurrent()) {setFound(page);setFailed(false);}}, () => {if (!abort.signal.aborted && isCurrent()) {setFound(undefined);setFailed(true);}});
    }, 250);
    return () => {clearTimeout(timer);abort.abort();};
  }, [source, query, state.phase, libraryKey, isCurrent]);
  return <section className={styles.editorSection}><h3>{t(excluded ? 'builder.neverInclude' : 'builder.alwaysInclude')}</h3>
    <div className={styles.choices}>{ids.slice(0, shown).map(id => {
      const title = source.title(id);
      return <Button key={id} size="small" variant="glass" disabled={locked} aria-label={`${title?.title ?? t('error.unavailable')} · ${t('builder.remove')}`} onClick={() => {if (isCurrent() && !locked) onChange(ids.filter(value => value !== id));}}>{title?.title ?? t('error.unavailable')}{title?.kind === 'show' ? ` · ${t('builder.pinnedShow')}` : ''} · {t('builder.remove')}</Button>;
    })}</div>
    {shown < ids.length && <Button size="small" variant="text" onClick={() => {if (isCurrent()) setShown(value => Math.min(ids.length, value + 20));}}>{t('action.showMore')}</Button>}
    <TextField label={t('builder.findTitle')} value={query} maxLength={200} disabled={locked || ids.length >= 500} onChange={event => {if (!isCurrent() || locked) return;source.cancelSearch();setFound(undefined);setFailed(false);setQuery(event.target.value);}}/>
    {found && <div className={styles.pinResults}>{found.length ? found.map(title => <Button key={title.id} size="small" variant="text" disabled={locked} onClick={() => {if (!isCurrent() || locked || ids.length >= 500 || !source.isSearchResult(title)) return;onChange([...ids.filter(id => id !== title.id), title.id]);source.cancelSearch();setFound(undefined);setQuery('');}}>{title.title}{title.kind === 'show' ? ` · ${t('builder.pinnedShow')}` : ''}</Button>) : <p className={styles.help}>{t('builder.noTitles')}</p>}</div>}
    {(failed || state.phase === 'error') && <p role="alert" className={styles.help}>{t('error.unavailable')}</p>}
  </section>;
}
