import {Fragment, useState} from 'react';
import {defaultI18n} from '@i18n';
import {importSkipped, liveSourceState} from '@core/server-admin/live-words.ts';
import {canRefreshSource} from '@core/server-admin/panel-logic.ts';
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
    {!snapshot.document && !snapshot.loading && <Button size="small" onClick={refresh}>{t('action.tryAgain')}</Button>}
    {!snapshot.document && snapshot.loading && <p role="status">{t('status.loadingThing', {thing: t('server.thing.liveSources')})}</p>}
    {snapshot.document && !snapshot.document.sources.length && <div className={styles.empty}><h4>{t('live.noSources')}</h4><p>{t('web.liveTv.noSourcesHelp')}</p><Button size="small" icon="i-plus" onClick={() => setAddOpen(true)}>{t('web.liveTv.addSource')}</Button></div>}
    {!!snapshot.document?.sources.length && <div className={styles.sources}>{snapshot.document.sources.map(source => {
      const status = snapshot.document!.refresh.find(value => value.sourceId === source.id);
      const state = liveSourceState(source, status);
      const notice = importSkipped(t, defaultI18n.has, source.diagnostics);
      // An upload is a saved manual import with no refresh loop, so it has no
      // refresh command even if a stale status entry named it.
      const refreshable = source.acquisition !== 'upload' && canRefreshSource(source, snapshot.document!.refresh);
      const facts = [t('server.live.channelCount', {count: source.channels}),
        source.programmes ? t('server.live.programCount', {count: source.programmes}) : '',
        source.tunerCount ? t(source.tunerCountMode === 'discovered' ? 'web.liveTv.tunersDetected' : 'server.live.tunerCount', {count: source.tunerCount}) : '',
        source.publishedAt ? t('server.live.guideUpdated', {when: defaultI18n.dateTime(source.publishedAt)}) : '',
        status?.errorCode ? t('web.console.refreshFailed') : '',
      ].filter(Boolean);
      return <Fragment key={source.id}><div className={styles.source} data-paused={source.state === 'disabled' || undefined}>
        <span className={styles.sourceIcon}><Icon name="i-tv" size={22}/></span>
        <div className={styles.facts}><b>{source.name}</b><p>{facts.map((fact, index) => <span key={index}>{fact}</span>)}</p></div>
        <span className={styles.badge} data-tone={state.tone} data-live={state.live || undefined} data-paused={source.state === 'disabled' || undefined} role={state.live ? 'status' : undefined}>{t(state.id)}</span>
        <Button size="small" disabled={!!snapshot.busy} onClick={() => configure(source)}>{t('web.liveTv.sourceSettings')}</Button>
        <Menu open={menu === source.id} onOpenChange={open => setMenu(open ? source.id : undefined)} title={source.name}
          trigger={<Button variant="icon" size="small" icon="i-more" iconOnly disabled={!!snapshot.busy} aria-label={t('server.row.actions', {name: source.name})}/>}
          items={[
            {id: 'settings', label: t('web.liveTv.sourceSettings'), onSelect: () => configure(source)},
            {id: 'channels', label: t('web.liveTv.channelsAndNumbers'), onSelect: () => channels(source)},
            {id: 'separator', separator: true},
            ...(refreshable ? [{id: 'refresh', label: t('web.liveTv.refreshGuideNow'), onSelect: () => act(source, 'refresh')}] : []),
            {id: 'toggle', label: t(source.state === 'disabled' ? 'web.liveTv.enableSource' : 'web.liveTv.pauseSource'), onSelect: () => act(source, 'toggle')},
            {id: 'remove', label: t('web.liveTv.removeSource'), destructive: true, onSelect: () => act(source, 'remove')},
          ]}/>
      </div>{notice && <div className={styles.notice}><Icon name="i-warn" size={18}/><div><b>{source.name}</b><p>{[notice.summary, ...notice.reasons].join(' ')}</p></div></div>}</Fragment>;
    })}</div>}
  </section>;
}
