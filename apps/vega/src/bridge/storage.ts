import {KeplerFileSystem as files} from '@amazon-devices/kepler-file-system';
import {sha256} from '@noble/hashes/sha2';
import {hex, utf8} from './encoding';

/**
 * The app's durable records: credentials, journals, queue records, the selected viewer, TV setup
 * keys and the profile trust vault. Apple keeps these in the keychain. The Vega SDK has no
 * keychain; what it gives an app is a private directory (`/data`) that no other app or service
 * can read, kept across restarts and updates and removed with the app. Records live there, one
 * per key, named by the same service and account the Apple modules use.
 *
 * The SDK's file API writes in place and cannot rename, so a write that is interrupted would
 * leave a damaged file. Each record therefore has two slots. A write goes to the slot that does
 * not hold the current version, with a sequence number and a checksum, and is read back before
 * the older slot is removed: at every moment one complete version exists.
 */
export type Service = 'queues' | 'credentials' | 'playback' | 'network' | 'profile-trust' | 'account-continuation' | 'top-shelf';

const ROOT = '/data';
const ENCODING = 'UTF-8';
const SLOTS = ['a', 'b'] as const;
type Slot = (typeof SLOTS)[number];
/** A slot's contents: the newest valid one is the record. `body` null marks a removed record. */
type Version = {slot: Slot; sequence: number; body: string | null};

const digest = (text: string) => hex(sha256(utf8(text)));
const base = (service: Service, account: string) => `${ROOT}/portico.${service}.${digest(`${service}\n${account}`).slice(0, 40)}`;

const tails = new Map<string, Promise<unknown>>();

/** Runs `task` after every earlier task for `lock` has settled: one read-modify-write at a time. */
export function serialized<T>(lock: string, task: () => Promise<T>): Promise<T> {
  const run = (tails.get(lock) ?? Promise.resolve()).catch(() => {}).then(task);
  tails.set(lock, run);
  void run.catch(() => {}).finally(() => { if (tails.get(lock) === run) tails.delete(lock); });
  return run;
}

function parse(slot: Slot, text: string): Version | null {
  const end = text.indexOf('\n');
  if (end < 0) return null;
  const [format, sequence, sum] = text.slice(0, end).split(' ');
  const number = Number(sequence);
  if (format !== 'v1' || !Number.isSafeInteger(number) || number < 1) return null;
  const body = text.slice(end + 1);
  if (sum === '-') return body === '' ? {slot, sequence: number, body: null} : null;
  return sum === digest(body) ? {slot, sequence: number, body} : null;
}

const encode = (sequence: number, body: string | null) => `v1 ${sequence} ${body === null ? '-' : digest(body)}\n${body ?? ''}`;

/** The newest complete version, and how many slot files exist at all. */
async function current(path: string, strict = false): Promise<{version: Version | null; present: Slot[]}> {
  let version: Version | null = null;
  const present: Slot[] = [];
  for (const slot of SLOTS) {
    if (!(await files.exists(`${path}.${slot}`))) continue;
    present.push(slot);
    const found = parse(slot, await files.readFileAsString(`${path}.${slot}`, ENCODING));
    if (found && (!version || found.sequence > version.sequence)) version = found;
  }
  // One damaged slot alone is a first write that never completed: there is no record. Two slots
  // with no complete version between them is damage, and must not read as "nothing saved".
  if (!version && (present.length === SLOTS.length || strict && present.length > 0)) throw new Error('The record is damaged.');
  return {version, present};
}

/** The record, or null when absent. Strict owners also reject one existing
 * damaged slot: a committed single-slot record must never reset to missing. */
export function readRecord(service: Service, account: string, options?: {strict?: boolean}): Promise<string | null> {
  const path = base(service, account);
  return serialized('record:' + path, async () => (await current(path, options?.strict)).version?.body ?? null);
}

/**
 * Writes (or with null, removes) a record and reads it back: a write the store acknowledged but
 * did not keep is a failure, as it is for the Apple modules' PIN counters and viewer selection.
 */
export function writeRecord(service: Service, account: string, value: string | null): Promise<void> {
  const path = base(service, account);
  return serialized('record:' + path, async () => {
    const {version, present} = await current(path);
    if (value === null && !version && present.length === 0) return;
    const slot: Slot = version?.slot === 'a' ? 'b' : 'a';
    const text = encode((version?.sequence ?? 0) + 1, value);
    await files.writeStringToFile(`${path}.${slot}`, text, ENCODING);
    if ((await files.readFileAsString(`${path}.${slot}`, ENCODING)) !== text) throw new Error('The record could not be confirmed in storage.');
    // The new version is durable: the older one (and, for a removal, the marker itself) can go.
    for (const other of SLOTS) if (other !== slot && present.includes(other)) await files.removeFile(`${path}.${other}`);
    if (value === null) await files.removeFile(`${path}.${slot}`);
  });
}
