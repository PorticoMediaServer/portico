/** One owner of the genuine device singleton. A replacement invalidates the
 * prior transport before any new viewer is adopted; late cleanup cannot clear it. */
const owners = new WeakMap<object, {token: object; retire: () => void}>();
export function claimNativeDownloadsDevice(device: object, retire: () => void) {
  const token = {}, previous = owners.get(device);
  owners.set(device, {token, retire});
  try {previous?.retire();} catch { /* The old capability is already invalid. */ }
  const current = () => owners.get(device)?.token === token;
  return Object.freeze({current, release: () => {if (current()) owners.delete(device);}});
}
