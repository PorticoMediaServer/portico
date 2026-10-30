import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';

const source = (path: string) => readFile(new URL(path, import.meta.url), 'utf8');

// AccountRoutes registers the profiles/servers renderers as module side effects.
// Metro enables inlineRequires, so a static import in RestoredShell stays lazy
// until its error branch and the ready shell has no renderer. The platform entry
// must therefore require the module eagerly.
test('the phone entry eagerly requires the accounts route registrations', async () => {
  const text = await source('../src-next/App.tsx');
  const start = text.indexOf('if (!Platform.isTV) {'), end = text.indexOf("require('./screens/home/CustomizeRoute')");
  assert.ok(start >= 0 && end > start, 'the phone registration block is present');
  assert.match(text.slice(start, end), /require\('\.\/screens\/accounts\/AccountRoutes'\)/);
});

test('the TV entry imports the accounts route registrations', async () => {
  assert.match(await source('../src-next/tv/register.ts'), /import '\.\.\/screens\/accounts\/AccountRoutes';/);
});
