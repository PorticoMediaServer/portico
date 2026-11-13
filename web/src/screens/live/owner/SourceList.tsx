import {Fragment, useState} from 'react';
import {defaultI18n} from '@i18n';
import {importSkipped, liveSourceState} from '@core/server-admin/live-words.ts';
import {Button} from '../../../kit/controls';
import {Menu} from '../../../kit/overlays';
import {Icon} from '../../../kit/Icon';
import type {OwnerLiveSource, SourceSnapshot} from './source-controller';
import styles from './owner.module.css';

const t = defaultI18n.t;
export type SourceListProps = Readonly<{
  snapshot: SourceSnapshot;
  refresh: () => void;
  add: (kind: 'remote' | 'discover' | 'upload') => void;
  configure: (source: OwnerLiveSource) => void;
  channels: (source: OwnerLiveSource) => void;
  act: (source: OwnerLiveSource, action: 'refresh' | 'toggle' | 'remove') => void;
}>;

/** The source's notice stays beside that source, rather than being detached
 * above the list. Names and counts are the checked server read, never fixtures. */
export function SourceList({snapshot, refresh, add, configure, channels, act}: SourceListProps) {
  const [addOpen, setAddOpen] = useState(false), [menu, setMenu] = useState<string>();
  return <section className={styles.section} aria-busy={snapshot.loading}>
    <div className={styles.heading}><h3>{t('web.liveTv.sources')}</h3><div className={styles.actions}>
      <Menu open={addOpen} onOpenChange={setAddOpen} title={t('web.liveTv.addSource')} width={340}
        trigger={<Button size="small" icon="i-plus">{t('web.liveTv.addSource')}<Icon name="i-down" size={16}/></Button>}
        items={[
          {id: 'remote', label: t('web.liveTv.remoteSource'), description: t('web.liveTv.remoteSourceMeta'), onSelect: () => add('remote')},
          {id: 'discover', label: t('web.liveTv.discover'), description: t('web.liveTv.discoverMeta'), onSelect: () => add('discover')},
          {id: 'upload', label: t('web.liveTv.upload'), description: t('web.liveTv.uploadMeta'), onSelect: () => add('upload')},
        ]}/>
    </div></div>
    <p className={styles.lede}>{t('next.liveOwner.sourcesLede')}</p>
    {!snapshot.document && !snapshot.loading && <Button size="small" onClick={refresh}>{t('action.tryAgain')}</Button>}
    {!snapshot.document && snapshot.loading && <p role="status">{t('status.loadingThing', {thing: t('server.thing.liveSources')})}</p>}
    {snapshot.document && !snapshot.document.sources.length && <div className={styles.empty}><span className={styles.emptyIcon} aria-hidden="true"><Icon name="i-tv" size={24}/></span><h4>{t('live.noSources')}</h4><p>{t('web.liveTv.noSourcesHelp')}</p><Button variant="primary" size="small" icon="i-plus" onClick={() => setAddOpen(true)}>{t('web.liveTv.addSource')}</Button></div>}
    {!!snapshot.document?.sources.length && <div className={styles.sources}>{snapshot.document.sources.map(source => {

      const state = liveSourceState(source, undefined);
      const notice = importSkipped(t, defaultI18n.has, source.report);
      const reasons = [...new Set([...(notice?.reasons ?? []), ...(source.report?.examples ?? []).filter(line => !line.startsWith('live.import.'))])];
      const refreshable = !source.paused && source.state !== 'refreshing';
      const facts = [source.report ? t('server.live.channelCount', {count: source.report.channels}) : '',
        source.report?.programmes ? t('server.live.programCount', {count: source.report.programmes}) : '',
        source.tunerCount ? t('server.live.tunerCount', {count: source.tunerCount}) : '',
        source.refreshedAt ? t('server.live.guideUpdated', {when: defaultI18n.dateTime(source.refreshedAt)}) : '',
        source.lastError ? t('web.console.refreshFailed') : '',
      ].filter(Boolean);
      return <Fragment key={source.id}><div className={styles.source} data-paused={source.paused || undefined}>
        <span className={styles.sourceIcon}><Icon name="i-tv" size={22}/></span>
        <div className={styles.facts}><b>{source.name}</b><p>{facts.map((fact, index) => <span key={index}>{fact}</span>)}</p></div>
        <span className={styles.badge} data-tone={state.tone} data-live={state.live || undefined} data-paused={source.paused || undefined} role={state.live ? 'status' : undefined}>{t(state.id)}</span>
        <Button size="small" disabled={!!snapshot.busy} onClick={() => configure(source)}>{t('web.liveTv.sourceSettings')}</Button>
        <Menu open={menu === source.id} onOpenChange={open => setMenu(open ? source.id : undefined)} title={source.name}
          trigger={<Button variant="icon" size="small" icon="i-more" iconOnly disabled={!!snapshot.busy} aria-label={t('server.row.actions', {name: source.name})}/>}
          items={[
            {id: 'settings', label: t('web.liveTv.sourceSettings'), onSelect: () => configure(source)},
            {id: 'channels', label: t('web.liveTv.channelsAndNumbers'), onSelect: () => channels(source)},
            {id: 'separator', separator: true},
            ...(refreshable ? [{id: 'refresh', label: t('web.liveTv.refreshGuideNow'), onSelect: () => act(source, 'refresh')}] : []),
            {id: 'toggle', label: t(source.paused ? 'web.liveTv.enableSource' : 'web.liveTv.pauseSource'), onSelect: () => act(source, 'toggle')},
            {id: 'remove', label: t('web.liveTv.removeSource'), destructive: true, onSelect: () => act(source, 'remove')},
          ]}/>
      </div>{notice && <div className={styles.notice}><Icon name="i-warn" size={18}/><div><b>{source.name}</b><p>{[notice.summary, ...reasons].join(' ')}</p></div></div>}</Fragment>;
    })}</div>}
  </section>;
}
