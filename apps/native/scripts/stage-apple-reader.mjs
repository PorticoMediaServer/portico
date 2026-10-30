import {constants} from 'node:fs';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import {pathToFileURL} from 'node:url';

const fileLimit = 64 * 1024 * 1024, totalLimit = 256 * 1024 * 1024, memberLimit = 4096;
const extensions = new Set(['.html', '.js', '.mjs', '.css', '.json', '.wasm', '.ttf', '.otf', '.woff', '.woff2', '.svg', '.png', '.jpg', '.jpeg', '.webp', '.ico', '.map']);
const invalid = () => { throw new Error('reader_bundle_invalid'); };

/** Stage only the producer's final, public build graph. No source build, network read or
 * selected-server content is performed by Xcode. Missing input leaves the bridge unavailable. */
export async function stageAppleReader(input, output) {
  const destination = path.resolve(output);
  if (path.basename(destination) !== 'PorticoReader.bundle') invalid();
  if (!input) {
    await fs.rm(destination, {recursive: true, force: true});
    return {available: false, files: 0};
  }
  const source = path.resolve(input);
  const within = (a, b) => b === a || b.startsWith(a + path.sep);
  if (within(source, destination) || within(destination, source)) invalid();
  const root = await fs.lstat(source);
  if (!root.isDirectory() || root.isSymbolicLink()) invalid();
  let count = 0, total = 0;
  const files = [];
  async function walk(directory, relative = '') {
    for (const name of await fs.readdir(directory)) {
      if (!/^[A-Za-z0-9_][A-Za-z0-9_.-]*$/.test(name)) invalid();
      const member = relative ? relative + '/' + name : name;
      const absolute = path.join(directory, name), info = await fs.lstat(absolute);
      if (info.isSymbolicLink()) invalid();
      if (info.isDirectory()) { await walk(absolute, member); continue; }
      if (!info.isFile() || !extensions.has(path.extname(name).toLowerCase()) || info.size > fileLimit) invalid();
      total += info.size;
      if (++count > memberLimit || total > totalLimit) invalid();
      files.push({member, size: info.size});
    }
  }
  await walk(source);
  const names = new Set(files.map(file => file.member));
  if (!names.has('index.html') || !names.has('licences.json') || !files.some(file => /^assets\/pdf\.worker-[^/]+\.mjs$/.test(file.member))) invalid();
  const parent = path.dirname(destination);
  await fs.mkdir(parent, {recursive: true});
  const temporary = await fs.mkdtemp(path.join(parent, '.portico-reader-'));
  try {
    for (const file of files) {
      const handle = await fs.open(path.join(source, file.member), constants.O_RDONLY | constants.O_NOFOLLOW);
      let bytes;
      try {
        const info = await handle.stat();
        if (!info.isFile() || info.size !== file.size) invalid();
        bytes = Buffer.alloc(file.size);
        let at = 0;
        while (at < bytes.length) {
          const {bytesRead} = await handle.read(bytes, at, bytes.length - at, at);
          if (!bytesRead) invalid();
          at += bytesRead;
        }
        if ((await handle.read(Buffer.alloc(1), 0, 1, at)).bytesRead) invalid();
      } finally { await handle.close(); }
      const target = path.join(temporary, file.member);
      await fs.mkdir(path.dirname(target), {recursive: true, mode: 0o755});
      await fs.writeFile(target, bytes, {mode: 0o644, flag: 'wx'});
    }
    const html = await fs.readFile(path.join(temporary, 'index.html'), 'utf8');
    const references = [...html.matchAll(/\b(?:src|href)\s*=\s*["']([^"']+)["']/gi)].map(match => match[1].replace(/^\.\//, ''));
    if (!references.length || references.some(reference => !names.has(reference))) invalid();
    JSON.parse(await fs.readFile(path.join(temporary, 'licences.json'), 'utf8'));
    await fs.rm(destination, {recursive: true, force: true});
    await fs.rename(temporary, destination);
    return {available: true, files: files.length};
  } finally { await fs.rm(temporary, {recursive: true, force: true}); }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  if (process.argv.length !== 3) throw new Error('reader_bundle_output_required');
  const result = await stageAppleReader(process.env.PORTICO_READER_BUNDLE_DIR, process.argv[2]);
  console.info(result.available ? `Reader bundle staged: ${result.files} public build files` : 'reader_bundle_absent: native reader unavailable');
}
