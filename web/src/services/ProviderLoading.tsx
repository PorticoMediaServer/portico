import wordmark from '../shell/wordmark.svg';
import {servicesI18n} from './i18n';

/** Match the document's first paint while a provider owns navigation. The
 * callback keeps its original #boot until a real destination is committed. */
export function ProviderLoading() {
  const {t} = servicesI18n();
  if (typeof document !== 'undefined' && document.getElementById('boot')) return null;
  return <div data-provider-loading role="status" aria-busy="true" aria-label={t('web.hostedSecurity.signingIn')}><img className="boot-wordmark" src={wordmark} alt="" /></div>;
}
