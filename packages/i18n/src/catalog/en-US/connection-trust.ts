/**
 * Exact server-identity copy (design b4cf92e, notes/server-identity.md).
 * Certificate decisions are headless; people compare an identity code only when required.
 */
export const connectionTrustMessages = {
  'connection.discovery.find': 'Find a server on this Wi-Fi',
  'connection.discovery.empty': 'No servers found on this network. Enter the server’s address, or check that it is running on the same Wi-Fi.',
  'connection.discovery.failed': 'Couldn’t search this network. Try again, or enter the server’s address.',

  'connection.certificates.title': 'Trusted certificates',
  'connection.certificates.forget': 'Forget certificate',
  'connection.certificates.confirm': 'Forget this certificate?',
  'connection.certificates.body': 'The next connection to {address} will check its certificate again. Your server sign-in stays on this device.',
  'connection.certificates.fingerprint': 'SHA-256 fingerprint',

  'connection.identity.changedBody': 'A different server is answering at its address, so nothing was sent. Check with its owner before you sign in again.',
  'connection.identity.forget': 'Forget {server}',
  'connection.identity.forgetTitle': 'Forget {server}?',
  'connection.identity.forgetBody': 'You’ll need to sign in again. Only do that if its owner says the server was reset.',
  'connection.identity.forgetAction': 'Forget',
  // Before the code matches, {server} is the address AS TYPED (69f2d45 frames 2a/2b/5).
  // No name or other route is requested before matching; only the proof exchange is allowed.
  'connection.identity.checkTitle': 'Check this is {server}',
  'connection.identity.checkBody': 'Ask its owner for the identity code in Settings › Remote access.',
  'connection.identity.codeLabel': 'Identity code',
  'connection.identity.checkHome': 'Or connect once at home instead.',
  'connection.identity.codeWrong': 'That code doesn’t match this server, so nothing was sent. Check it with its owner.',
  'connection.certificate.unsupported': 'If this server uses its own certificate: this TV accepts only certificates from a public authority. Use the server’s http:// address on your home network, or turn on its secure connection.',
  'connection.plainHttp.appleRemote': 'On Apple devices, plain HTTP works only on your home network. Use this server’s https:// address.',
} as const;
