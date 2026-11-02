import {useShellFacts} from '../../shell/fact-hooks';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {useScreenAppearance} from '../browse/screen-preferences';
import {HomeScreen} from './Home';
import {FirstLibraryState} from './first-library-state';
import {MemberEmptyState} from './member-empty-state';
import {useMusicMixes} from '../browse/music-mixes-hook';
import {MusicDiscoverMore} from '../browse/music-discover-navigation';
import {Banner} from '../../kit/overlays';
import {defaultI18n} from '@i18n';

function HomeRouteScreen() {
  const captured = useProtectedWebScreenContext();
  useShellFacts(captured?.context);
  const appearance = useScreenAppearance(captured?.context);
  const context = captured?.context;
  const musicMixes = useMusicMixes(captured, !!context?.facts.identity.libraries.some(library => library.kind === 'music'));
  const identity = context?.facts.identity;
  // A library directory that did not answer is not "no libraries": never the empty states then.
  const unknown = identity?.librariesUnavailable === true;
  const notice = unknown && context ? <Banner icon="i-warn" action={{label: defaultI18n.t('next.states.tryAgain'), onAction: () => {if (context.isCurrent()) context.retryLibraries?.();}}}>
    {defaultI18n.t('next.home.librariesUnavailable')}</Banner> : undefined;
  if (!unknown && captured && context?.isCurrent() && context.session().viewer.role === 'owner' && identity?.primaryProfile === true
    && identity.capabilities?.manageServer === true && identity.libraries.length === 0 && !identity.liveAvailable) return <FirstLibraryState captured={captured}/>;
  if (!unknown && captured && context?.isCurrent() && context.session().viewer.role !== 'owner'
    && identity?.libraries.length === 0 && !identity.liveAvailable) return <MemberEmptyState captured={captured}/>;
  return captured ? <HomeScreen {...captured.binding.home} notice={notice} appearance={appearance} musicMixes={musicMixes} musicMixesMore={<MusicDiscoverMore captured={captured}/>}/> : null;
}

export default defineWebScreen({id: 'home', component: HomeRouteScreen});
