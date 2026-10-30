import {readerAssetEntry} from './asset-origin';

/** DEV probes contain no API, publication, error text, URLs or session packets. */
export const readerStartupProbe = `(function(){
  if(window.__porticoReaderProbe)return;
  window.__porticoReaderProbe=true;
  function report(phase){
    var root=document.getElementById('root');
    window.ReactNativeWebView&&window.ReactNativeWebView.postMessage(JSON.stringify({
      kind:'reader-startup-diagnostic',phase:phase,
      entry:location.href==='portico-reader://bundle/index.html'||location.href==='https://appassets.androidplatform.net/PorticoReader.bundle/index.html',
      bridge:!!window.PorticoReader,root:!!root,children:root?Math.min(root.childElementCount,100):0,
      scripts:Math.min(document.scripts.length,100)
    }));
  }
  window.addEventListener('error',function(){report('error');},true);
  window.addEventListener('unhandledrejection',function(){report('rejection');});
  document.addEventListener('securitypolicyviolation',function(){report('csp');});
  document.addEventListener('DOMContentLoaded',function(){report('dom');});
  window.addEventListener('load',function(){report('load');setTimeout(function(){report('settled');},1000);});
  report('injected');
})();true;`;

/** Ignore untrusted fields even on a trusted document; never log a raw packet. */
export function readerStartupDiagnostic(url: string, message: string): Readonly<Record<string, string | boolean | number>> | undefined {
  if (!readerAssetEntry(url) || message.length > 1024) return;
  let value: unknown;
  try {value = JSON.parse(message);} catch {return;}
  if (!value || typeof value !== 'object' || Array.isArray(value)) return;
  const data = value as Record<string, unknown>;
  if (data.kind !== 'reader-startup-diagnostic' || !['injected', 'dom', 'load', 'settled', 'error', 'rejection', 'csp'].includes(String(data.phase))) return;
  if (typeof data.entry !== 'boolean' || typeof data.bridge !== 'boolean' || typeof data.root !== 'boolean') return;
  if (!Number.isInteger(data.children) || Number(data.children) < 0 || Number(data.children) > 100 || !Number.isInteger(data.scripts) || Number(data.scripts) < 0 || Number(data.scripts) > 100) return;
  return {phase: String(data.phase), entry: data.entry, bridge: data.bridge, root: data.root, children: Number(data.children), scripts: Number(data.scripts)};
}

/** DEV bridge observations reconstruct only protocol vocabulary, never packet data. */
export function readerBridgeDiagnostic(message: string): Readonly<{kind: string; method?: string; failed: boolean}> | undefined {
  if (typeof message !== 'string' || message.length > 2 * 1024 * 1024) return;
  let value: unknown;
  try {value = JSON.parse(message);} catch {return;}
  if (!value || typeof value !== 'object' || Array.isArray(value)) return;
  const data = value as Record<string, unknown>;
  if (data.v !== 1 || typeof data.kind !== 'string' || !['ready', 'initial', 'request', 'reply', 'active', 'preferences', 'retired', 'cancel', 'sequential-cancel'].includes(data.kind)) return;
  const method = data.kind === 'request' && typeof data.method === 'string'
    && ['head', 'range', 'asset', 'page', 'sequential-open', 'sequential-read', 'chunk', 'release', 'progress', 'save', 'back', 'settings'].includes(data.method)
    ? data.method : undefined;
  return {kind: data.kind, ...(method ? {method} : {}), failed: Object.prototype.hasOwnProperty.call(data, 'error')};
}
