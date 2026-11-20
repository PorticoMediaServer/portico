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
    [`../../../kit/saved/${parts}`]: kit, [`../../shared/saved/${hookName}`]: hook,
  });
  return {[`${kind}Screen`]: page[pageName]};
}
