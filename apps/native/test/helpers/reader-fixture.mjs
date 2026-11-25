import {screenModule} from './screen-harness.mjs';

export async function readerFixtureModule(name, previous) {
  const lookup = path => previous[path];
  const bindings = Object.fromEntries(Object.entries(previous).map(([path, value]) => {
    if (path.startsWith('../../../../../packages/')) return ['../' + path, value];
    if (path.startsWith('../../kit/') || path.startsWith('../../platform/')) return ['../' + path, value];
    if (path.startsWith('../downloads/')) return [path.replace('../downloads/', '../../../screens/downloads/'), value];
    if (path.startsWith('./')) return [path.replace('./', '../../../screens/read/'), value];
    return [path, value];
  }));
  const load = (file, modules) => screenModule(new URL(`../../src-next/${file}`, import.meta.url), modules);
  const react = lookup('react');
  const back = await load('kit/reading/ReaderBack.tsx', {
    react, 'react-native': lookup('react-native'), 'react-native-safe-area-context': lookup('react-native-safe-area-context'),
    '../controls/Button': lookup('../../kit/controls/Button'),
  });
  const frame = await load('kit/frames/ReaderFrame.tsx', {
    react,
    '../navigation/stack-slot': lookup('../../kit/navigation/stack-slot'), '../shell/Layer': lookup('../../kit/shell/Layer'),
    '../focus/CoveredLayer': lookup('../../kit/focus/CoveredLayer') ?? {CoveredLayer: 'CoveredLayer'},
    '../states': lookup('../../kit/states'), '../reading/ReaderBack': back, './PageStateFrame': {PageStateFrame: 'StateView'},
  });
  const isGuest = name === 'Guest';
  const hook = await load(`pages/shared/reading/useReader${isGuest ? 'Guest' : 'Session'}.ts`, {
    ...bindings, '../../../kit/reading/ReadingGuestView': lookup('./ReadingGuestView'),
  });
  const page = await load(`pages/touch/reading/Reader${isGuest ? 'Guest' : ''}Page.tsx`, {
    ...bindings, '../../../kit/frames/ReaderFrame': frame, '../../../kit/reading/ReadingGuestView': lookup('./ReadingGuestView'),
    './ReaderGuestPage': {ReaderGuestPage: lookup('./NativeReadingGuestHost')?.NativeReadingGuestHost},
    '../../shared/reading/useReaderGuest': hook, '../../shared/reading/useReaderSession': hook,
  });
  return isGuest ? {NativeReadingGuestHost: page.ReaderGuestPage, ...frame} : {ReadNativeHost: page.ReaderPage, ...frame};
}
