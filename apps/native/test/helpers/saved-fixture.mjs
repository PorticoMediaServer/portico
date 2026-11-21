import {screenModule} from './screen-harness.mjs';

const load = (path, bindings) => screenModule(new URL(`../../src-next/${path}`, import.meta.url), bindings);

export async function savedFixture(kind, old) {
  const tv = kind === 'TVSaved';
  const downloads = kind === 'Downloads';
  const pageName = tv ? 'TVSavedPage' : `${kind}Page`;
  const hookName = downloads ? 'useDownloads' : tv ? 'useTVSavedPage' : 'useSavedPage';
  const hookBindings = {};
  const kitBindings = {};
  const pageBindings = {};
  for (const [path, value] of Object.entries(old)) {
    if (!path.startsWith('.')) {
      hookBindings[path] = kitBindings[path] = pageBindings[path] = value;
    } else if (path.includes('packages/')) {
      const name = path.slice(path.indexOf('packages/'));
      hookBindings[`../../../../../../${name}`] = pageBindings[`../../../../../../${name}`] = kitBindings[`../../../../../${name}`] = value;
    } else if (path.includes('/kit/')) {
      const name = path.split('/kit/')[1];
      hookBindings[`../../../kit/${name}`] = pageBindings[`../../../kit/${name}`] = kitBindings[`../${name}`] = value;
    } else {
      const name = path.replace(/^\.\.\//, '').replace(/^\.\//, '');
      if (name.includes('platform/')) hookBindings[`../../../platform/${name.split('/platform/').at(-1).replace(/^platform\//, '')}`] = value;
      else if (path.endsWith('/shell/model')) hookBindings['../../../shell/model'] = value;
      else if (tv && ['focus', 'useEntryFocus', 'useTVPageBack'].includes(name)) hookBindings[`../../../screens/destinations/${name}`] = value;
      else if (path.endsWith('/tv/layout/TVFocusGrid')) kitBindings['../../tv/layout/TVFocusGrid'] = value;
      else {
        const section = downloads ? 'downloads' : 'saved';
        const screen = name.startsWith('../') ? name.replace(/^(?:\.\.\/)+/, '') : name.includes('/') ? name : `${section}/${name}`;
        hookBindings[`../../../screens/${screen}`] = pageBindings[`../../../screens/${screen}`] = kitBindings[`../../screens/${screen}`] = value;
      }
    }
  }
  const hook = await load(`pages/shared/saved/${hookName}.ts`, hookBindings);
  const parts = downloads ? 'DownloadInventory' : tv ? 'TVSavedParts' : 'SavedParts';
  kitBindings[`../../pages/shared/saved/${hookName}`] = hook;
  const kit = await load(`kit/saved/${parts}.tsx`, kitBindings);
  const page = await load(`pages/${tv ? 'tv' : 'touch'}/saved/${downloads ? 'Downloads' : 'Saved'}Page.tsx`, {
    ...pageBindings, '../../../kit/frames': {BrowseFrame: pageName},
    ...(tv ? {'./SavedDirectoryPage': old['./SavedDirectoryPage']} : {}),
    '../../../kit/saved/SavedDownloadCard': old['./SavedDownloadCard'],
    [`../../../kit/saved/${parts}`]: kit, [`../../shared/saved/${hookName}`]: hook,
  });
  return {[`${kind}Screen`]: page[pageName]};
}

export async function tvSavedDirectoryFixture(bindings) {
  const react = bindings.react;
  const i18n = bindings['../../../../../packages/i18n/src'];
  const model = await load('pages/shared/saved/useTVSavedDirectory.ts', {
    react, '../../../../../../packages/i18n/src': i18n,
    '../../../../../../packages/client-core/src/presentation/errors': bindings['../../../../../packages/client-core/src/presentation/errors'],
    '../../../kit/focus': bindings['../../kit/focus'], '../../../screens/saved/presentation': bindings['./presentation'],
  });
  const kit = await load('kit/saved/SavedTVDirectoryParts.tsx', {
    react, 'react-native': bindings['react-native'], '../../../../../packages/i18n/src': i18n,
    '../../pages/shared/saved/useTVSavedDirectory': model, '../../screens/saved/presentation': bindings['./presentation'],
    '../theme': bindings['../../kit/theme'], '../focus': bindings['../../kit/focus'], '../Icon': bindings['../../kit/Icon'],
    '../artwork': bindings['../../kit/artwork'], '../cards/primitives': bindings['../../kit/cards/primitives'], '../controls': {Button: 'Button'},
    '../states': bindings['../../kit/states'], '../browse/presentation': bindings['../../kit/browse/presentation'],
    '../live/GuideProgrammeTitle': {GuideProgrammeTitle: 'GuideProgrammeTitle'},
    './SavedTVLoading': bindings['./SavedTVLoading'], './PlaylistToolbar': bindings['./PlaylistToolbar'],
  });
  const page = await load('pages/tv/saved/SavedDirectoryPage.tsx', {
    react, '../../../../../../packages/i18n/src': i18n, '../../shared/saved/useTVSavedDirectory': model,
    '../../../kit/saved/SavedForms': bindings['../../kit/saved/SavedForms'], '../../../kit/saved/SavedTVDirectoryParts': kit,
  });
  return {SavedTVDirectory: props => {
    const [body, sheet] = page.SavedTVDirectory(props).props.children;
    return [kit.SavedTVDirectoryBody(body.props), sheet];
  }};
}

export async function savedResourceFixture(bindings) {
  const i18n = bindings['../../../../../packages/i18n/src'];
  const common = {
    react: bindings.react, 'react-native': bindings['react-native'], '../../../../../../packages/i18n/src': i18n,
    '../../../../../../packages/client-core/src/saved': bindings['../../../../../packages/client-core/src/saved'],
    '../../../screens/saved/presentation': bindings['./presentation'],
  };
  const model = await load('pages/shared/saved/useSavedResource.ts', {
    ...common, '../../../../../../packages/client-core/src/presentation/errors': bindings['../../../../../packages/client-core/src/presentation/errors'],
    '../../../screens/read/platform': bindings['../read/platform'], '../../../shell/model': bindings['../../shell/model'],
  });
  const page = await load('pages/shared/saved/SavedResourcePage.tsx', {
    ...common, './useSavedResource': model, '../../../screens/destinations/focus': bindings['../destinations/focus'],
    '../../../kit/frames/SavedResourceFrame': {SavedResourceFrame: 'SavedResourceFrame'},
    '../../../kit/saved/SavedResourceParts': {SavedResourceAction: 'SavedResourceAction', SavedResourceBand: 'SavedResourceBand',
      SavedResourceCaption: 'SavedResourceCaption', SavedResourceHero: 'SavedResourceHero', SavedResourceHoldHint: 'SavedResourceHoldHint',
      SavedResourceInventory: 'SavedResourceInventory', SavedResourceMenuFacts: 'SavedResourceMenuFacts',
      SavedResourceRow: function SavedResourceRow() {}, SavedResourceUndo: 'SavedResourceUndo', SavedResourceViewGrid: 'SavedResourceViewGrid',
      SavedResourceViewInfo: 'SavedResourceViewInfo', SavedResourceVisibility: 'SavedResourceVisibility',
      useSavedResourceLayout: () => ({width: 350, columns: 1, rowStride: 80, windowWidth: 390, gutter: 20}), viewSortLabel: () => ''},
    '../../../kit/frames': {PanelBlock: 'PanelBlock'},
    '../../../kit/saved/SavedReorderList': bindings['./SavedReorderList'],
    ...Object.fromEntries(Object.entries(bindings).filter(([key]) => key.startsWith('../../kit/')).map(([key, value]) => [`../${key}`, value])),
  });
  return {SavedResourcePage: props => {
    const frame = page.SavedResourcePage(props);
    return [frame, frame.props.footer, frame.props.sheets];
  }};
}
