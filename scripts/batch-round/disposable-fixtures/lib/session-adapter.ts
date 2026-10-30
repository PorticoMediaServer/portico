/** Private stdio bridge: Python fixture orchestration, canonical playback clients. */
import {createInterface} from 'node:readline';
import {readFile, lstat, mkdir} from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import {join} from 'node:path';
import type {LocalSession} from '../../../../packages/client-core/src/index.ts';
import type {CapabilityDocument} from '../../../../packages/client-core/src/client-profile.ts';
import {CapabilityPublisher} from '../../../../packages/client-core/src/playback-v1/options.ts';
import {ObservationReporter} from '../../../../packages/client-core/src/playback-v1/observation.ts';
import type {Session} from '../../../../packages/client-core/src/playback-v1/types.ts';
import type {SessionChange, StartOptions} from '../../../../packages/client-core/src/playback-v1/sessions.ts';
import {FileMutationStore, lockMutationDirectory, mutationClient, finalStop} from '../../load-adapters.ts';

async function privateJSON(path: string) {
  const info = await lstat(path);
  if (!info.isFile() || info.isSymbolicLink() || (info.mode & 0o777) !== 0o600 || info.uid !== process.getuid?.()) throw Error('private_input_required');
  if (info.size > 4 * 1024 * 1024) throw Error('private_input_too_large');
  return JSON.parse(await readFile(path, 'utf8'));
}

async function main() {
  if (process.versions.node !== '24.21.0') throw Error('node_24_21_0_required');
  const [target, statePath, capabilitiesPath] = process.argv.slice(2);
  const session = await privateJSON(statePath) as LocalSession;
  if (!session.installationId || !session.deviceId) throw Error('session_device_required');
  const installationId = session.installationId, deviceId = session.deviceId;
  const capabilities = await privateJSON(capabilitiesPath) as CapabilityDocument;
  const directory = statePath + '.mutations';
  await mkdir(directory, {mode: 0o700, recursive: true});
  const unlock = await lockMutationDirectory(directory);
  const transport: typeof fetch = async (input, init = {}) => {
    if (new URL(String(input)).origin !== new URL(target).origin) throw Error('cross_origin_refused');
    const headers = new Headers(init.headers);
    headers.set('Origin', target);
    headers.set('X-Portico-Installation-Id', installationId);
    return fetch(input, {...init, headers, redirect: 'error', credentials: 'omit'});
  };
  const client = await mutationClient(target, session, new FileMutationStore(join(directory, 'commands.json')), transport);
  const active = new Map<string, {session: Session; reporter: ObservationReporter<Session>; position: number}>();
  const stop = async (id: string, position?: number) => {
    const entry = active.get(id);
    if (!entry) throw Error('session_not_owned');
    entry.reporter.stop();
    await finalStop(client.sessions, id, entry.session.presentation.generation)(position ?? entry.position, AbortSignal.timeout(5000));
    active.delete(id);
  };
  const lines = createInterface({input: process.stdin, crlfDelay: Infinity});
  const commands = lines[Symbol.asyncIterator]();
  try {
    if (await new CapabilityPublisher(client.http, () => deviceId).publish(capabilities, AbortSignal.timeout(10000)) === 'failed') throw Error('capability_publication_failed');
    for await (const line of commands) {
      if (line.length > 65536) throw Error('command_too_large');
      try {
        const command = JSON.parse(line);
        let result: unknown;
        const signal = AbortSignal.timeout(120000);
        if (command.op === 'start') {
          const view = await client.sessions.start({itemId: command.itemId}, command.options as StartOptions, randomUUID(), {signal});
          const entry = {session: view, position: command.options.startPositionMs ?? 0, reporter: new ObservationReporter<Session>({
            everyMs: 1000, claim: body => client.sessions.claimObserver(view.id, body, AbortSignal.timeout(5000)),
            send: sample => client.sessions.observe(view.id, sample, AbortSignal.timeout(5000))})};
          entry.reporter.setGeneration(view.presentation.generation);
          entry.reporter.observe({generation: view.presentation.generation, state: 'playing', positionMs: entry.position, rate: 1});
          active.set(view.id, entry);
          result = view;
        } else if (command.op === 'get') {
          result = await client.sessions.get(command.id, signal);
        } else if (command.op === 'change') {
          const entry = active.get(command.id);
          if (!entry) throw Error('session_not_owned');
          const current = await client.sessions.get(command.id, signal);
          const view = await client.sessions.patch(command.id, current.revision, command.change as SessionChange, signal);
          entry.session = view;
          entry.position = command.change.seek?.positionMs ?? command.change.positionMs ?? entry.position;
          entry.reporter.setGeneration(view.presentation.generation);
          entry.reporter.observe({generation: view.presentation.generation, state: view.state === 'paused' ? 'paused' : 'playing', positionMs: entry.position, rate: 1});
          result = view;
        } else if (command.op === 'stop') {
          await stop(command.id, command.positionMs);
          result = null;
        } else throw Error('unknown_command');
        process.stdout.write(JSON.stringify({ok: true, result}) + '\n');
      } catch (error) {
        const {status, code} = error as {status?: number; code?: string};
        process.stdout.write(JSON.stringify({ok: false, status: Number.isInteger(status) ? status : 500, code: typeof code === 'string' && /^[a-z0-9_]{1,80}$/.test(code) ? code : 'canonical_session_adapter_failed'}) + '\n');
      }
    }
  } finally {
    lines.close();
    let failed = false;
    for (const [id, entry] of active) {
      entry.reporter.stop();
      try {await stop(id);} catch {failed = true;}
    }
    client.coordinator.dispose();
    await unlock();
    if (failed) process.exitCode = 1;
  }
}
main().catch(() => {process.stderr.write('canonical session adapter refused\n'); process.exitCode = 1;});
