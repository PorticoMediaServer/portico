import {createHash} from 'node:crypto';

export type NativeClient = 'android' | 'apple';
export type NativeNpmEntry = {name: string; version: string; platforms: NativeClient[]; licence: string; licenceFile: string; licenceSHA256: string; url: string};
export type NativePackage = {version: string; license?: string; repository?: string | {url?: string}};

const repositoryURL = (repository: NativePackage['repository']) => (typeof repository === 'string' ? repository : repository?.url)?.replace(/^git\+/, '').replace(/\.git$/, '').replace(/\/$/, '');

/** Autolinking, not mere installation, determines which native npm code is shipped. */
export function nativeNpmComponents(client: NativeClient, inventory: NativeNpmEntry[], linked: ReadonlyMap<string, readonly NativeClient[]>, represented: ReadonlySet<string>, installed: (name: string) => NativePackage, licenceText: (name: string, file: string) => string) {
  const seen = new Set<string>();
  for (const entry of inventory) {
    if (!/^(@[a-z0-9-]+\/)?[a-z0-9._-]+$/.test(entry.name) || !entry.platforms.length || entry.platforms.some(p => !['android', 'apple'].includes(p))) throw new Error('Invalid native npm platform or name.');
    if (!/^(?:MIT|BSD-2-Clause|BSD-3-Clause|Apache-2.0|ISC|MPL-2.0|LGPL-.*|GPL-.*)$/.test(entry.licence)) throw new Error(`Unsupported native npm licence: ${entry.name}`);
    if (!/^https:\/\//.test(entry.url) || !/^[A-Za-z0-9._-]+$/.test(entry.licenceFile)) throw new Error(`Invalid native npm provenance: ${entry.name}`);
    for (const platform of entry.platforms) {
      const key = entry.name + ':' + platform;
      if (seen.has(key)) throw new Error(`Duplicate native npm registration: ${key}`);
      seen.add(key);
      if (!linked.get(entry.name)?.includes(platform)) throw new Error(`Native npm module is not autolinked: ${key}`);
    }
    const pkg = installed(entry.name), text = licenceText(entry.name, entry.licenceFile);
    if (pkg.version !== entry.version || pkg.license !== entry.licence || repositoryURL(pkg.repository) !== repositoryURL(entry.url) || createHash('sha256').update(text).digest('hex') !== entry.licenceSHA256) throw new Error(`Native npm provenance changed: ${entry.name}`);
  }
  for (const [name, platforms] of linked) if (platforms.includes(client) && !represented.has(name) && !seen.has(name + ':' + client)) throw new Error(`Shipped native npm module is unlisted: ${name} (${client})`);
  return inventory.filter(e => e.platforms.includes(client) && !represented.has(e.name)).map(entry => ({id: `native-npm:${entry.name}@${entry.version}`, name: entry.name, version: entry.version, licence: entry.licence, group: client, text: licenceText(entry.name, entry.licenceFile), url: entry.url}));
}
