import {useShellFacts} from '../../shell/fact-hooks';
import {useState} from 'react';
import {defaultI18n} from '@i18n';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext, useWebAction} from '../../route-registry-view';
import {currentWebEnvironment} from '../../platform/environment';
import {Button} from '../../kit/controls';
import {Licences} from '../../kit/states/Licences';
import {router} from '../../router';
import tmdbLogo from '../../kit/assets/tmdb.svg';
import styles from './about.module.css';
const t = defaultI18n.t;
function About() {
  const captured = useProtectedWebScreenContext(), feedback = useWebAction('feedback');
  useShellFacts(captured?.context);
  const [licenses, setLicenses] = useState(false);
  if (!captured) return null;
  const identity = captured.context.facts.identity;
  const {appVersion: version, buildId} = currentWebEnvironment();
  return <main className={styles.page}>
    <Button variant="text" icon="i-left" onClick={() => {void router.navigate({to: '/settings', search: {scope: 'account', section: 'device'}});}}>{t('next.settingsSpec.account.device.title')}</Button>
    <h1>{t('settings.about')}</h1><p className={styles.lede}>{t('licenses.portico.body')}</p>
    <dl className={styles.box}>
      <div><dt>{t('web.about.webApp')}</dt><dd>{version || t('next.states.versionNotRecorded')}</dd></div>
      {buildId && <div><dt>{t('web.about.build')}</dt><dd>{buildId}</dd></div>}
      <div><dt>{t('web.about.server')}</dt><dd>{identity.serverName}</dd></div>
      <div><dt>{t('web.about.serverVersion')}</dt><dd>{identity.serverVersion || t('next.states.versionNotRecorded')}</dd></div>
      {identity.serverApiVersion && <div><dt>{t('web.about.serverApi')}</dt><dd>{identity.serverApiVersion}</dd></div>}
    </dl>
    <section className={styles.box}>
      <button type="button" onClick={() => setLicenses(true)}><b>{t('licenses.title')}</b><span>{t('licenses.rowHelp')}</span></button>
      <a href="https://github.com/PorticoMediaServer/portico-server" target="_blank" rel="noopener noreferrer">{t('licenses.source.title')}</a>
    </section>
    <section className={styles.box}>
      <a href="https://getportico.tv/docs/" target="_blank" rel="noopener noreferrer">{t('web.about.help')}</a>
      <a href="https://getportico.tv/terms" target="_blank" rel="noopener noreferrer">{t('web.about.terms')}</a>
      <a href="https://getportico.tv/privacy" target="_blank" rel="noopener noreferrer">{t('web.about.privacy')}</a>
    </section>
    {feedback && <section className={styles.box}><Button variant="glass" icon="i-flag" onClick={feedback}>{t('next.shell.feedback')}</Button></section>}
    <p className={styles.legal}><img src={tmdbLogo} alt="TMDB" height={16}/>{t('web.about.legal')} {t('web.about.tmdb')}</p>
    <p className={styles.legal}>{t('about.datasetCredit')}</p>
    <Licences open={licenses} onOpenChange={setLicenses}/>
  </main>;
}
export default defineWebScreen({id: 'about', component: About});
