/**
 * en-US copy for the secure connection: the Server settings section that says whether devices
 * reach the server encrypted and how to turn that on, first-run setup's Portico Account step,
 * and the sign-in screens' note about plain HTTP. Merged into `enUS` statically.
 */
export const secureConnectionMessages = {
  // ── Server settings › Remote access › Secure connection ──
  'settings.server.secure.title': 'Secure connection',
  'settings.server.secure.lede': 'Encrypts everything between your devices and this server, at home and away, with a certificate every browser and app trusts. Portico gets it and renews it for you.',
  'settings.server.secure.toggle': 'Use a secure connection',
  'settings.server.secure.toggleHelp': 'When this is off, devices connect with plain HTTP. They still work; their connection just isn’t encrypted.',
  'settings.server.secure.port': 'Secure connection port',
  'settings.server.secure.portHelp': 'The port devices use for the secure address from outside your home.',
  'settings.server.secure.badgeOn': 'On',
  'settings.server.secure.badgeOff': 'Off',
  'settings.server.secure.badgeStarting': 'Starting',
  'settings.server.secure.badgeProblem': 'Needs attention',
  'settings.server.secure.badgeWaiting': 'Waiting',
  'settings.server.secure.on': 'Devices connect to this server securely, at home and away.',
  'settings.server.secure.onAt': 'Devices connect to this server securely, at home and away. Its secure address is {address}.',
  'settings.server.secure.own': 'Devices connect securely with your own certificate.',
  'settings.server.secure.ownAt': 'Devices connect securely to {address} with your own certificate.',
  'settings.server.secure.starting': 'Portico is getting a certificate for this server. This usually takes a minute or two; nothing needs doing meanwhile.',
  'settings.server.secure.off': 'Devices connect with plain HTTP. Turn on “Use a secure connection” to encrypt their connection again.',
  'settings.server.secure.waiting': 'Approve this server in your Portico Account to finish. The certificate follows by itself.',
  'settings.server.secure.notSetUp': 'Devices connect with plain HTTP. To turn on a secure connection, connect this server to your Portico Account: Portico then gets and renews a certificate by itself. Or use your own certificate, below.',
  'settings.server.secure.notSetUpOwnOnly': 'Devices connect with plain HTTP. To turn on a secure connection, use your own certificate, below.',
  'settings.server.secure.connect': 'Turn on secure connection',
  'settings.server.secure.connectHelp': 'Opens your Portico Account in a new window, where you approve this server. That’s all: the certificate follows by itself.',
  'settings.server.secure.connectWithAccount': 'Turn on with {account}',
  'settings.server.secure.connectWithAccountHelp': 'Connects this server to your Portico Account and gets its certificate. Nothing else to do.',
  'settings.server.secure.connectInBrowser': 'Turn on in a browser',
  'settings.server.secure.connectInBrowserHelp': 'Opens this server’s settings in your browser, where you approve it in your Portico Account. Or sign in to your Portico Account in this app to do it here.',
  'settings.server.secure.connected': 'Connected. Portico is getting the certificate now.',
  'settings.server.secure.invalid': 'The certificate is invalid.',
  'settings.server.secure.renewalFailed': 'The certificate is still working, but its renewal failed.',
  'settings.server.secure.obtainFailed': 'The server could not obtain a certificate.',
  'settings.server.secure.publishFailed': 'The secure address could not be published.',
  'settings.server.secure.problemUnknown': 'The secure connection needs attention. Check the server logs for details.',

  // ── First-run setup with a Portico Account ──
  'web.setup.hostedBodySecure': 'One sign-in for all your apps, easy invitations, access away from home and a secure connection, set up for you.',
  'web.setup.nextConnectSecure': 'Next, you approve this server in your Portico Account. It then gets its secure connection by itself.',
  'web.setup.connectTitle': 'Approve {server} in your Portico Account',
  'web.setup.connectLede': 'A Portico Account window is open. Sign in there if you need to, and approve this server. This page carries on when you do.',
  'web.setup.connectOpen': 'Open Portico Account',
  'web.setup.connectLater': 'Do this later',
  'web.setup.connectLaterHelp': 'You can connect any time in Settings › Remote access. Until then, devices connect with plain HTTP.',
  'web.setup.connectRetry': 'Start again',

  // ── First run from the Portico app on a phone or tablet ──
  'setup.app.found': 'Nobody has set up this server yet. You can set it up from here.',
  'setup.app.start': 'Set up this server',
  'setup.app.nextWithAccount': 'Next, this server joins {account} and gets its secure connection by itself.',
  'setup.app.connecting': 'Connecting {server} to {account}',
  'setup.app.connectLede': 'Your Portico Account opens in the browser. Sign in there if you need to, approve this server, then come back here.',
  'setup.app.notSetUp': 'This server isn’t set up yet. Set it up from the Portico app on a phone or tablet, or open its address in a browser.',

  // ── Signed-out screens over plain HTTP from outside the home ──
  'signIn.plainHttp.owner': 'The server’s owner can turn on a secure connection in Settings › Remote access.',
} as const;
