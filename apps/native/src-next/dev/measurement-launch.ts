/** Public, DEV-only measurement launch selection. It contains no app state,
 * server address, arbitrary fixture props or authentication material. */
export type NativeMeasurementLaunch = Readonly<
  {kind: 'normal'} | {kind: 'public'; specimen: 'signed-out-welcome'} | {kind: 'refused'}
>;
const normal: NativeMeasurementLaunch = Object.freeze({kind: 'normal'});
const refused: NativeMeasurementLaunch = Object.freeze({kind: 'refused'});
const welcome: NativeMeasurementLaunch = Object.freeze({kind: 'public', specimen: 'signed-out-welcome'});

export function nativeMeasurementLaunch(url: string | null): NativeMeasurementLaunch {
  if (!url) return normal;
  const attempted = /^portico:\/*(?:[^/?#]*@)?native-layout/i.test(url.trim());
  let value: URL;
  try {value = new URL(url);} catch {return attempted ? refused : normal;}
  if (value.hostname.toLowerCase() !== 'native-layout') return attempted ? refused : normal;
  if (value.protocol !== 'portico:' || value.port || value.username || value.password || value.pathname || value.hash) return refused;
  if ([...value.searchParams.keys()].some(key => key !== 'specimen') || value.searchParams.getAll('specimen').length !== 1) return refused;
  return value.searchParams.get('specimen') === 'signed-out-welcome' ? welcome : refused;
}
