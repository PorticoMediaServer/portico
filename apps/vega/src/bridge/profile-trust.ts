import {cbc} from '@noble/ciphers/aes';
import {hmac} from '@noble/hashes/hmac';
import {sha256} from '@noble/hashes/sha2';
import {base64, concat, fromBase64, fromUtf8, hex, sameBytes, utf8} from './encoding';
import {failure} from './events';
import {pbkdf2Sha256} from './kdf';
import {randomBytes, uuid} from './random';
import {readRecord, serialized, writeRecord} from './storage';

/**
 * The profile trust vault (`Native/PorticoProfileTrust.h`): proofs that let a trusted profile be
 * chosen on this device, each sealed with a key made from the profile's PIN, a device key and
 * the profile's scope. The rules are the Apple module's:
 *
 * - every caller runs one at a time, including failed-attempt writes;
 * - a PIN attempt is charged in storage before the expensive check, so a restart cannot reset the
 *   delay; corrupt counters fail closed;
 * - the device key behind existing proofs is never replaced;
 * - no PIN or derived key is stored.
 */
const LOCK = 'profile-trust';
const ITERATIONS = 200000;
const controls = /[\u0000-\u001f\u007f-\u009f]/;

type Stored = {version?: unknown; scope?: unknown; metadata?: unknown; salt?: unknown; iv?: unknown; cipher?: unknown; mac?: unknown; attempts?: unknown; lockedUntil?: unknown};
type Metadata = Record<string, unknown>;

const read = (key: string) => readRecord('profile-trust', key);
const write = (key: string, value: string | null) => writeRecord('profile-trust', key, value);
const storage = (message: string) => failure('secure_storage', message);

function text(value: unknown, max: number): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= max && !controls.test(value);
}

function parse(json: string | null): unknown {
  if (json === null) return undefined;
  try { return JSON.parse(json); } catch { return undefined; }
}

const isObject = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const isNumber = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);

function attemptsValid(record: Stored): boolean {
  const {attempts, lockedUntil} = record;
  return isNumber(attempts) && attempts >= 0 && attempts <= 1000 && Math.floor(attempts) === attempts && isNumber(lockedUntil) && lockedUntil >= 0;
}

async function installation(): Promise<string> {
  const held = await read('installation');
  if (held !== null) {
    if (!text(held, 128)) throw new Error('invalid');
    return held;
  }
  const made = uuid();
  await write('installation', made);
  return made;
}

async function deviceKey(create: boolean): Promise<Uint8Array | null> {
  const held = await read('device-key');
  if (held !== null) {
    const bytes = fromBase64(held);
    if (!bytes || bytes.length !== 32) throw new Error('invalid');
    return bytes;
  }
  if (!create) return null;
  // Never replace the key behind existing proofs: a new one is made only for an empty vault.
  const index = await read('index.v1');
  if (index !== null) {
    const rows = parse(index);
    if (!Array.isArray(rows) || rows.length !== 0) throw new Error('invalid');
  }
  const key = randomBytes(32);
  await write('device-key', base64(key));
  return key;
}

function recordKey(scope: unknown): string | null {
  if (!text(scope, 2048)) return null;
  const parts = parse(scope);
  if (!Array.isArray(parts) || parts.length !== 6 || parts[0] !== 'portico-profile-trust-v1' || (parts[1] !== 'hosted' && parts[1] !== 'local')) return null;
  for (let i = 2; i < 6; i++) if (!text(parts[i], 128)) return null;
  return 'profile:' + hex(sha256(utf8(scope)));
}

/** `pinRequired` is a JSON boolean; the Apple module also takes a number. */
const pinFlag = (value: unknown) => typeof value === 'boolean' || isNumber(value);
const pinRequired = (value: unknown) => value === true || (isNumber(value) && value !== 0);

function pinValid(pin: unknown, required: boolean): pin is string {
  if (typeof pin !== 'string') return false;
  return required ? /^[0-9]{4}$/.test(pin) : pin.length === 0;
}

async function keys(scope: string, pin: string, salt: Uint8Array, create: boolean): Promise<Uint8Array | null> {
  const device = await deviceKey(create);
  if (!device) return null;
  const material = concat(utf8(pin), device, utf8(scope));
  try { return await pbkdf2Sha256(material, salt, ITERATIONS, 64); } finally { material.fill(0); }
}

function mac(scope: string, metadata: string, salt: Uint8Array, iv: Uint8Array, cipher: Uint8Array, key: Uint8Array): Uint8Array {
  const tag = hmac.create(sha256, key);
  for (const part of [utf8(scope), utf8(metadata), salt, iv, cipher]) {
    // Each part is preceded by its length as a 64-bit big-endian count.
    const length = new Uint8Array(8);
    new DataView(length.buffer).setUint32(4, part.length);
    tag.update(length).update(part);
  }
  return tag.digest();
}

async function index(): Promise<string[]> {
  const raw = await read('index.v1');
  if (raw === null) return [];
  const rows = parse(raw);
  if (!Array.isArray(rows) || rows.length > 64) throw new Error('invalid');
  for (const key of rows) if (!text(key, 80) || !key.startsWith('profile:')) throw new Error('invalid');
  return rows as string[];
}

/** The index without expired proofs, which are removed: they must not fill the 64-profile vault. */
async function activeIndex(): Promise<string[]> {
  const rows = await index();
  const next: string[] = [];
  const now = Date.now();
  for (const key of rows) {
    const raw = await read(key);
    if (raw === null) continue;
    const record = parse(raw);
    if (!isObject(record) || !text(record.metadata, 8192)) throw new Error('invalid');
    const metadata = parse(record.metadata);
    const expires = isObject(metadata) ? metadata.expiresAtMs : undefined;
    if (!isNumber(expires) || expires <= 0) throw new Error('invalid');
    if (expires <= now) await write(key, null); else next.push(key);
  }
  if (next.length !== rows.length) await write('index.v1', JSON.stringify(next));
  return next;
}

function metadataValid(scope: string, metadata: unknown, install: string): metadata is Metadata {
  const parts = parse(scope);
  if (!isObject(metadata) || !Array.isArray(parts) || parts.length !== 6) return false;
  if (metadata.authority !== parts[1] || metadata.accountId !== parts[2] || metadata.serverId !== parts[3] || metadata.profileId !== parts[4] || metadata.installationId !== parts[5] || metadata.installationId !== install) return false;
  if (!text(metadata.profileName, 120) || !text(metadata.serverName, 2048) || !text(metadata.serverUrl, 4096) || !pinFlag(metadata.pinRequired) || !isNumber(metadata.expiresAtMs)) return false;
  const now = Date.now();
  return metadata.expiresAtMs > now && metadata.expiresAtMs <= now + 30 * 86400000 + 60000;
}

async function seal(scope: string, metadataJSON: string, payloadJSON: string, pin: string): Promise<Stored> {
  const plain = utf8(payloadJSON);
  if (plain.length > 32768 || !isObject(parse(payloadJSON))) throw new Error('invalid');
  const salt = randomBytes(32), iv = randomBytes(16);
  const derived = await keys(scope, pin, salt, true);
  if (!derived) throw new Error('unavailable');
  try {
    const cipher = cbc(derived.subarray(0, 32), iv).encrypt(plain);
    const tag = mac(scope, metadataJSON, salt, iv, cipher, derived.subarray(32));
    return {version: 1, scope, metadata: metadataJSON, salt: base64(salt), iv: base64(iv), cipher: base64(cipher), mac: base64(tag), attempts: 0, lockedUntil: 0};
  } finally {
    derived.fill(0);
  }
}

/** The payload, `'pin'` when the PIN is wrong, or null when the record cannot be read. */
async function open(record: Stored, scope: string, pin: string): Promise<string | 'pin' | null> {
  if (record.version !== 1 || record.scope !== scope || !text(record.metadata, 8192)) return null;
  for (const key of ['salt', 'iv', 'cipher', 'mac'] as const) if (!text(record[key], 65536)) return null;
  const salt = fromBase64(record.salt as string), iv = fromBase64(record.iv as string), cipher = fromBase64(record.cipher as string), tag = fromBase64(record.mac as string);
  if (!salt || !iv || !cipher || !tag || salt.length !== 32 || iv.length !== 16 || cipher.length < 16 || cipher.length > 32784 || tag.length !== 32) return null;
  const derived = await keys(scope, pin, salt, false);
  if (!derived) return null;
  try {
    if (!sameBytes(tag, mac(scope, record.metadata, salt, iv, cipher, derived.subarray(32)))) return 'pin';
    let plain: Uint8Array;
    try { plain = cbc(derived.subarray(0, 32), iv).decrypt(cipher); } catch { return null; }
    const json = fromUtf8(plain);
    plain.fill(0);
    return json !== null && isObject(parse(json)) ? json : null;
  } finally {
    derived.fill(0);
  }
}

export const profileTrust = {
  installation(): Promise<string> {
    return serialized(LOCK, async () => {
      try { return await installation(); } catch { throw storage("This device's profile installation could not be read."); }
    });
  },

  save(scope: string, metadataJSON: string, payloadJSON: string, pin: string): Promise<boolean> {
    return serialized(LOCK, async () => {
      const key = recordKey(scope);
      const install = await installation().catch(() => null);
      const metadata = parse(typeof metadataJSON === 'string' ? metadataJSON : null);
      if (!key || !install || !metadataValid(scope, metadata, install) || !pinValid(pin, pinRequired(metadata.pinRequired))) {
        throw failure('profile_trust_invalid', 'A current, fully scoped selection and its PIN are required to trust this profile.');
      }
      let rows: string[];
      try { rows = await activeIndex(); } catch { throw storage('Trusted profiles could not be read.'); }
      if (rows.length >= 64 && !rows.includes(key)) throw failure('profile_trust_full', 'Remove an unused trusted profile before remembering another.');
      let record: Stored;
      try { record = await seal(scope, metadataJSON, payloadJSON, pin); } catch { throw storage('The profile proof could not be protected on this device.'); }
      let old: string | null;
      try { old = await read(key); } catch { throw storage('The existing profile proof could not be read.'); }
      try {
        await write(key, JSON.stringify(record));
        if (!rows.includes(key)) await write('index.v1', JSON.stringify([...rows, key]));
      } catch {
        // Put back what was there: a half-saved proof must not replace a working one.
        await write(key, old).catch(() => {});
        await write('index.v1', JSON.stringify(rows)).catch(() => {});
        throw storage('This profile selection could not be remembered.');
      }
      return true;
    });
  },

  list(): Promise<string> {
    return serialized(LOCK, async () => {
      let rows: string[];
      try { rows = await activeIndex(); } catch { throw storage('Trusted profiles could not be read.'); }
      const out: unknown[] = [];
      const now = Date.now();
      for (const key of rows) {
        let raw: string | null;
        try { raw = await read(key); } catch { throw storage('A trusted profile could not be read.'); }
        if (raw === null) continue;
        const record = parse(raw);
        const metadata = isObject(record) && text(record.metadata, 8192) ? parse(record.metadata) : undefined;
        if (!isObject(metadata)) throw storage('A trusted profile record is invalid.');
        if (isNumber(metadata.expiresAtMs) && metadata.expiresAtMs > now) out.push(metadata);
      }
      return JSON.stringify(out);
    });
  },

  unlock(scope: string, pin: string): Promise<string> {
    return serialized(LOCK, async () => {
      const key = recordKey(scope);
      if (!key) throw failure('profile_trust_invalid', 'Choose a previously trusted profile.');
      const value = parse(await read(key).catch(() => null));
      if (!isObject(value)) throw failure('profile_trust_missing', 'This profile is not available for offline selection on this device.');
      const record = value as Stored;
      if (!text(record.metadata, 8192) || !attemptsValid(record)) throw storage('The trusted profile record is invalid. Select it online again.');
      const metadata = parse(record.metadata);
      const install = await installation().catch(() => null);
      if (!install || !metadataValid(scope, metadata, install)) throw failure('profile_trust_expired', 'This trusted profile expired or belongs to another installation. Select it online again.');
      const now = Date.now() / 1000;
      if ((record.lockedUntil as number) > now) throw failure('profile_pin_locked', 'Profile PIN attempts are temporarily locked. Try again later.');
      if (!pinValid(pin, pinRequired(metadata.pinRequired))) throw failure('profile_pin_required', "Enter this profile's four-digit PIN.");
      // Charge the attempt before the expensive check.
      const attempts = Math.min(1000, (record.attempts as number) + 1);
      const delay = attempts >= 10 ? 900 : attempts >= 5 ? 300 : attempts >= 3 ? 30 : 2;
      const charged: Stored = {...record, attempts, lockedUntil: now + delay};
      try { await write(key, JSON.stringify(charged)); } catch { throw storage('The profile unlock attempt could not be saved.'); }
      const payload = await open(charged, scope, pin).catch(() => null);
      if (payload === 'pin') throw failure('profile_pin_required', 'That profile PIN is not correct. Try again after the delay.');
      if (payload === null) throw storage('The protected profile proof could not be read.');
      try { await write(key, JSON.stringify({...charged, attempts: 0, lockedUntil: 0})); } catch { throw storage('The profile unlock could not be saved.'); }
      return payload;
    });
  },

  remove(scope: string): Promise<boolean> {
    return serialized(LOCK, async () => {
      const key = recordKey(scope);
      if (!key) throw failure('profile_trust_invalid', 'A complete profile scope is required.');
      try {
        await write(key, null);
        const rows = await index();
        await write('index.v1', JSON.stringify(rows.filter(row => row !== key)));
      } catch {
        throw storage('The trusted profile could not be removed.');
      }
      return true;
    });
  },
};
