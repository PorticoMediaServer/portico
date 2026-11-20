import {screenModule} from './screen-harness.mjs';

/**
 * HomeScreen's row cards (`pages/shared/home/home-cards.tsx`) and its upright-phone page (`pages/touch/home/HomePage.tsx`
 * on `pages/shared/home/useHome.ts`) are separate modules now. A test that loads HomeScreen with stubbed imports passes
 * the same stubs here: they are re-keyed from HomeScreen's folder to the pages' depth, the real modules are loaded
 * against them, and they are added under HomeScreen's own import paths.
 */
const rekey = key => key
  .replace(/^\.\.\/\.\.\/\.\.\/\.\.\/\.\.\/packages\//, '../../../../../../packages/')
  .replace(/^\.\.\/\.\.\/(kit|platform|shell)(\/|$)/, '../../../$1$2')
  .replace(/^\.\.\/destinations\//, '../../../screens/destinations/')
  .replace(/^\.\/([\w-]+)$/, '../../../screens/home/$1');
const defaults = {
  '../../../kit/cards/caption-metrics': {cardTitleLine: () => 18}, '../../../screens/home/generated-poster': {generatedPoster: () => null},
  '../../../platform/ArtworkPipeline': {ArtworkPlacementContext: {Provider: 'Provider'}}, '../../../platform/home-action-diagnostics': {homeActionDiagnostics: {action() {}}},
  '../../../../../../packages/client-core/src/server-clock': {serverNow: () => Date.now()},
};

export async function withHomeCards(stubs) {
  const deep = {...defaults, ...Object.fromEntries(Object.entries(stubs).map(([key, value]) => [rekey(key), value]))};
  const cards = await screenModule(new URL('../../src-next/pages/shared/home/home-cards.tsx', import.meta.url), deep);
  const useHome = await screenModule(new URL('../../src-next/pages/shared/home/useHome.ts', import.meta.url), {...deep, './home-cards': cards});
  const page = await screenModule(new URL('../../src-next/pages/touch/home/HomePage.tsx', import.meta.url), {...deep, '../../shared/home/home-cards': cards, '../../shared/home/useHome': useHome});
  return {...stubs, '../../pages/shared/home/home-cards': cards, '../../pages/touch/home/HomePage': page};
}

/** HomeScreen is a one-line switch (an upright phone draws HomePage, other forms HomeScreenForms); tests that call it
 * as a function in a hook host expand that one element, so they read the drawn tree as before. */
export function expandHome(module) {
  return {...module, HomeScreen: props => {const element = module.HomeScreen(props); return element && typeof element.type === 'function' ? element.type(element.props) : element;}};
}
