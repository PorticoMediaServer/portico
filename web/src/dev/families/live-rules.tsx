import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {defineGalleryFamily} from '../gallery-registry';
import {SeriesRules} from '../../screens/live/SeriesRules';
import {seriesRulesFixture} from './live-rules.fixture';
import {Icon} from '../../kit/Icon';
import {ProfileAvatarFace, TopBarActivity} from '../../kit/shell';
import wordmark from '../../shell/wordmark.svg';
import shell from '../../shell/shell.module.css';
import styles from '../../kit/dvr/rules.module.css';
const t = defaultI18n.t;
function GalleryNavigation() {
  return <nav className={`${shell.topnav} ${styles.galleryNavigation}`} aria-label={t('next.home.shell.main')}>
    <a className={shell.brand} href="#"><img src={wordmark} alt={t('next.platform.appName')}/></a><ul className={shell.destinations}>
      {(['next.home.shell.home', 'search.chip.movies', 'next.settingsSpec.server.libraries.panels.newLibrary.tabs.0.groups.0.rows.1.control.options.1.label', 'web.libraries.kindAnime', 'pref.group.music.title', 'search.chip.audiobooks', 'next.home.shell.live'] as const).map(key => <li key={key}><a href="#" aria-current={key === 'next.home.shell.live' ? 'page' : undefined}>{t(key)}</a></li>)}
    </ul><div className={shell.tools}>
      <button className={shell.icon} aria-label={t('next.home.shell.search')}><Icon name="i-search" size={22}/></button>
      <button className={shell.icon} aria-label={t('next.home.shell.saved')}><Icon name="i-bookmark" size={22}/></button>
      <button className={shell.icon} aria-label={t('next.home.shell.unread', {count: 3})}><Icon name="i-bell" size={22}/><span className={shell.dot}/></button>
      <TopBarActivity viewerRole="owner" open={false} onOpenChange={() => {}} view={{serverName: seriesRulesFixture.sourceName, streamCount: 2, streams: [], scans: [], tasks: [], phase: 'ready', onRetry() {}, onDashboard() {}}}/>
      <span className={shell.avatar}><ProfileAvatarFace initial="D" size={34}/></span>
    </div>
  </nav>;
}
function RulesFrame() {
  const [view, setView] = useState(seriesRulesFixture);
  return <SeriesRules view={view} navigation={<GalleryNavigation/>}
    onSave={async (rule, settings) => {setView(previous => ({...previous, selectedRuleId: undefined, rules: previous.rules.map(value => value.id === rule.id ? {...value, settings} : value)}));return true;}}
    onDelete={async rule => {setView(previous => ({...previous, selectedRuleId: undefined, rules: previous.rules.filter(value => value.id !== rule.id)}));return true;}}/>;
}
export default defineGalleryFamily({id: 'live-rules', labelKey: 'recordings.rules', cases: [{id: 'editor', labelKey: 'next.live.seriesRule', width: 1440, height: 950, render: RulesFrame}]});
