import {screenModule} from './screen-harness.mjs';

/**
 * HomeScreen draws Home's rows and hero facts from the kit (`kit/home`). A test that loads HomeScreen with stubbed
 * imports passes the same stubs here: `kit/home` sits as deep as `screens/home`, so only HomeScreen's `../../kit/…`
 * paths are re-keyed to the kit's own `../…`, the real kit modules are loaded against them, and `kit/home` is added
 * under HomeScreen's import path.
 */
const rekey = key => key.replace(/^\.\.\/\.\.\/kit\//, '../');
const defaults = {
  '../cards/caption-metrics': {cardTitleLine: () => 18}, '../../platform/ArtworkPipeline': {ArtworkPlacementContext: {Provider: 'Provider'}},
  '../../platform/home-action-diagnostics': {homeActionDiagnostics: {action() {}}}, '../../../../../packages/client-core/src/server-clock': {serverNow: () => Date.now()},
};

export async function withHomeCards(stubs, actualSpotlight = false) {
  const kit = {...defaults, ...Object.fromEntries(Object.entries(stubs).map(([key, value]) => [rekey(key), value]))};
  if (kit['../artwork'] && !kit['../artwork'].generatedPoster) kit['../artwork'] = {...kit['../artwork'], generatedPoster: () => null};
  // The card words and play target come from client-core in the kit; a test's own HomeScreen `./model` stub supplies them.
  const model = stubs['./model'] ?? await import('../../../../packages/client-core/src/presentation/home-card.ts');
  kit['../../../../../packages/client-core/src/presentation/home-card'] ??= {homeCardText: model.homeCardText, homePlayTarget: model.homePlayTarget};
  // The drawing's Home sizes are real data: a test's `@portico/design` stub keeps its own keys and gains `homeRowsSpec`.
  kit['@portico/design'] = {homeRowsSpec: (await import('@portico/design')).homeRowsSpec, ...kit['@portico/design']};
  const theme = kit['../theme'].useTheme;
  const design = await import('@portico/design');
  const dimensions = () => stubs['react-native'].useWindowDimensions?.() ?? {width: 390, height: 844};
  const layout = {useFrameMetrics: () => ({form: theme().form ?? 'phone', width: dimensions().width, gutter: theme().gutter,
    rightGutter: theme().gutter})};
  kit['../frames/metrics'] = layout;
  kit['../focus'] ??= {};
  const rows = await screenModule(new URL('../../src-next/kit/home/HomeRows.tsx', import.meta.url), kit);
  const facts = await screenModule(new URL('../../src-next/kit/home/HeroFacts.tsx', import.meta.url), kit);
  kit['../frames/HomeHero'] ??= {homeLockupStyle: () => ({})};
  const parts = await screenModule(new URL('../../src-next/kit/home/HomeParts.tsx', import.meta.url), kit);
  const metrics = await screenModule(new URL('../../src-next/kit/home/metrics.ts', import.meta.url), {...kit, './HomeRows': rows});
  const home = {...rows, ...facts, ...parts, ...metrics, HomeSpotlight: actualSpotlight ? parts.HomeSpotlight : 'Hero'};
  // The upright phone's page (pages/touch/home) and its hook (pages/shared/home) sit one folder deeper than HomeScreen.
  const deeper = key => key.replace(/^\.\.\/\.\.\/\.\.\/\.\.\/\.\.\/packages\//, '../../../../../../packages/')
    .replace(/^\.\.\/\.\.\/(kit|platform|shell)(\/|$)/, '../../../$1$2').replace(/^\.\.\/destinations\//, '../../../screens/destinations/')
    .replace(/^\.\/([\w-]+)$/, '../../../screens/home/$1');
  const page = {...Object.fromEntries(Object.entries(stubs).map(([key, value]) => [deeper(key), value])),
    '@portico/design': {...design, ...stubs['@portico/design']}, '../../../kit/home': home, '../../../kit/frames/metrics': layout,
    '../../../kit/home/HomeTVParts': {TVHomeSpotlight: 'TVHomeSpotlight',
      TVHomeRow: 'TVHomeRow', TVHomeCustomize: 'TVHomeCustomize'},
    '../../../kit/cards/primitives': {Parts: 'Parts'}};
  const useTVHome = await screenModule(new URL('../../src-next/pages/shared/home/useTVHome.ts', import.meta.url), page);
  const tvHomePage = await screenModule(new URL('../../src-next/pages/tv/home/HomePage.tsx', import.meta.url), {...page,
    '../../shared/home/useTVHome': useTVHome,
    '../../shared/home/useTVHomeRecoveryView': {useTVHomeRecoveryView: () => ({retry: {}, facts: [], opener: {current: null}})}});
  const useHome = await screenModule(new URL('../../src-next/pages/shared/home/useHome.ts', import.meta.url), page);
  const homePage = await screenModule(new URL('../../src-next/pages/touch/home/HomePage.tsx', import.meta.url), {...page, '../../shared/home/useHome': useHome});
  return {...stubs, '../../kit/home': home, '../../pages/touch/home/HomePage': homePage, '../../pages/tv/home/HomePage': tvHomePage};
}

/** HomeScreen is a switch (touch draws HomePage, TV its own HomePage); tests that call it as a
 * function in a hook host expand that one element, so they read the drawn tree. */
export function expandHome(module) {
  return {...module, HomeScreen: props => {const element = module.HomeScreen(props); return element && typeof element.type === 'function' ? element.type(element.props) : element;}};
}
