import test from 'node:test';
import assert from 'node:assert/strict';
import {namedSettingsForm} from '../src/server-admin/server-forms.ts';
import {presentError} from '../src/presentation/errors.ts';
import {defaultI18n} from '../../i18n/src/index.ts';

test('a stale Intros edit retains the refusal and newer value, rather than overwriting the other owner', async () => {
  let revision = 1, values = {shortestEpisodeMinutes: 10, shortestFilmMinutes: 30};
  const writes: unknown[] = [];
  const document = () => ({name: 'segments', etag: `"${revision}"`, values});
  const api = {
    request: async <T>() => document() as T,
    requestRaw: async (_path: string, _method: string, options: any) => {
      writes.push(options);
      if (options.headers['If-Match'] !== document().etag) return {status: 412, headers: {}, body: {error: {code: 'changed_elsewhere'}}};
      values = {...values, ...options.body}; revision++;
      return {status: 200, headers: {}, body: document()};
    },
  };
  const form = namedSettingsForm({api, console: {} as never, serverId: 'server', operationId: () => 'unused'}, 'segments');
  try {
    await form.load(); form.set({shortestEpisodeMinutes: 20});
    values = {...values, shortestEpisodeMinutes: 15}; revision++;
    await assert.rejects(form.save(), {code: 'changed_elsewhere'});
    assert.equal(form.getSnapshot().saved?.shortestEpisodeMinutes, 15);
    assert.equal(values.shortestEpisodeMinutes, 15);
    assert.equal(presentError(form.getSnapshot().saveError, 'settings', {operation: 'save'}).body, defaultI18n.t('error.changed'));
    assert.equal(writes.length, 1);
    assert.deepEqual((writes[0] as any).headers, {'If-Match': '"1"'});
    assert.deepEqual((writes[0] as any).body, {shortestEpisodeMinutes: 20});
    form.discard(); form.set({shortestFilmMinutes: 45}); await form.save();
    assert.deepEqual(values, {shortestEpisodeMinutes: 15, shortestFilmMinutes: 45});
  } finally {form.dispose();}
});

test('the title data form reads and saves the rebuilt server\'s title_data document, naming only the change', async () => {
  const {createServerForm} = await import('../src/server-admin/server-forms.ts');
  const sent: unknown[] = [];
  const api = {
    request: async <T>(path: string, method?: string) => {sent.push([method, path]); return {name: 'title_data', etag: '"4"', values: {download: true}, defaults: {download: true}} as T;},
    requestRaw: async (path: string, method: string, options: any) => {sent.push([method, path, options.headers['If-Match'], options.body]); return {status: 200, headers: {}, body: {name: 'title_data', etag: '"5"', values: {download: false}}};},
  };
  const form = createServerForm('titleData', {api, console: {} as never, serverId: 'server', operationId: () => 'unused'});
  try {
    await form.load(); assert.equal(form.getSnapshot().saved?.download, true);
    form.set((draft: Record<string, unknown>) => ({...draft, download: false})); await form.save();
    assert.deepEqual(sent, [['GET', '/v1/admin/settings/title_data'], ['PUT', '/v1/admin/settings/title_data', '"4"', {download: false}]]);
    assert.equal(form.getSnapshot().saved?.download, false);
  } finally {form.dispose();}
});
