/** One document, one origin. At web.getportico.tv the Portico Account pages (sign-in, the
 * account workspace, links and claims) are the Services entry; everything else, the server
 * chooser included, is the app itself talking directly to the chosen server. A server's own
 * address serves only the app, and loads nothing here before it. Nothing here navigates to
 * another origin. */
async function boot() {
  if (import.meta.env.VITE_HOSTED_WEB === 'true') {
    const {hostedServicesPage} = await import('./services/mode');
    if (await hostedServicesPage(location)) {await import('./services/main'); return;}
  }
  await import('./app-main');
}
void boot();
