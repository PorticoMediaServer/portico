import {setCredentialLock, setInstallationIdentity, validInstallationId, type InstallationIdentity} from '@core/installation.ts';
import {browserInstallation} from './profile-trust';

// The canonical profile-trust service owns this record and creates it only on first use.
// Startup reads it directly to distinguish absence from corrupt or unwritable storage.
const installationKey = 'portico.profile-installation.v1';

/** Pure device-description logic; no component or old UI dependency. */
function browserDeviceName(userAgent: string): string {
  const browser = /Edg\//.test(userAgent) ? 'Edge' : /OPR\//.test(userAgent) ? 'Opera' : /Firefox\//.test(userAgent) ? 'Firefox' : /Chrome\//.test(userAgent) ? 'Chrome' : /Safari\//.test(userAgent) ? 'Safari' : 'Web browser';
  const system = /iPhone/.test(userAgent) ? 'iPhone' : /iPad/.test(userAgent) ? 'iPad' : /Android/.test(userAgent) ? 'Android' : /Mac OS X/.test(userAgent) ? 'Mac' : /Windows/.test(userAgent) ? 'Windows' : /CrOS/.test(userAgent) ? 'Chromebook' : /Linux/.test(userAgent) ? 'Linux' : '';
  return system ? `${browser} on ${system}` : browser;
}

/**
 * Call once after platform/polyfills and before restoring or requesting credentials.
 * Failed storage leaves the claim unset: an ephemeral replacement must never be used to
 * renew a credential bound to the persisted installation. No credential store is changed.
 */
export function initializeBrowserInstallation(appVersion: string): InstallationIdentity | undefined {
  // Polyfills already supply the accepted per-tab implementation on plain HTTP.
  setCredentialLock((name, work) => navigator.locks.request(name, work));
  let installationId: string;
  try {
    const saved = localStorage.getItem(installationKey);
    // Even an empty persisted value is damage, not a new installation to create silently.
    if (saved !== null && !validInstallationId(saved)) return;
    installationId = browserInstallation();
    if (!validInstallationId(installationId) || (saved !== null && saved !== installationId) || localStorage.getItem(installationKey) !== installationId) return;
  } catch { return; }
  const identity: InstallationIdentity = {installationId, name: browserDeviceName(navigator.userAgent), platform: 'web', app: 'Portico Web', appVersion};
  setInstallationIdentity(identity);
  return identity;
}
