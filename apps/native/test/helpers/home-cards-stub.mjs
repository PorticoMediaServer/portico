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

export async function withHomeCards(stubs) {
  const kit = {...defaults, ...Object.fromEntries(Object.entries(stubs).map(([key, value]) => [rekey(key), value]))};
  if (kit['../artwork'] && !kit['../artwork'].generatedPoster) kit['../artwork'] = {...kit['../artwork'], generatedPoster: () => null};
  // The card words and play target come from client-core in the kit; a test's own HomeScreen `./model` stub supplies them.
  const model = stubs['./model'] ?? await import('../../../../packages/client-core/src/presentation/home-card.ts');
  kit['../../../../../packages/client-core/src/presentation/home-card'] ??= {homeCardText: model.homeCardText, homePlayTarget: model.homePlayTarget};
  // The drawing's Home sizes are real data: a test's `@portico/design` stub keeps its own keys and gains `homeRowsSpec`.
  kit['@portico/design'] = {homeRowsSpec: (await import('@portico/design')).homeRowsSpec, ...kit['@portico/design']};
  const rows = await screenModule(new URL('../../src-next/kit/home/HomeRows.tsx', import.meta.url), kit);
  const facts = await screenModule(new URL('../../src-next/kit/home/HeroFacts.tsx', import.meta.url), kit);
  return {...stubs, '../../kit/home': {...rows, ...facts}};
}
