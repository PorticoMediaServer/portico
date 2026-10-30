/**
 * BETA06: iPhone and iPad downloads are owned by the app, not by React Native, so a background
 * relaunch for download events (no scene, no React Native) still recreates the session, files what
 * arrived and only then releases the system's completion handler.
 *
 * These are source assertions only. There is no Apple compiler on the Linux hosts, so nothing here
 * compiles or runs the Objective-C or Swift: they check the native source's wiring and ordering.
 * The lifecycle itself (an OS-terminated app relaunched for download events, the locked-since-boot
 * case, suspended wakes) needs device verification and has not been device-tested.
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';

const downloads = readFileSync(new URL('../Native/PorticoDownloads.m', import.meta.url), 'utf8');
const delegate = readFileSync(new URL('../ios/Portico/AppDelegate.swift', import.meta.url), 'utf8');
const bridging = readFileSync(new URL('../Native/Portico-Bridging-Header.h', import.meta.url), 'utf8');
const ownerHeader = readFileSync(new URL('../Native/PorticoDownloadOwner.h', import.meta.url), 'utf8');

function section(source: string, start: string, end: string): string {
  const from = source.indexOf(start);
  assert.notEqual(from, -1, start);
  const to = source.indexOf(end, from + start.length);
  assert.notEqual(to, -1, end);
  return source.slice(from, to);
}

function method(source: string, signature: string): string {
  return section(source, signature, '\n}\n');
}

/** Source without comments, string and character literals, for counting brackets. */
function code(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/\/\/.*$/gm, '')
    .replace(/@?"(?:\\.|[^"\\\n])*"/g, '""')
    .replace(/'(?:\\.|[^'\\\n])'/g, "''");
}

test('the native sources keep balanced brackets', () => {
  for (const [name, source] of [['PorticoDownloads.m', downloads], ['AppDelegate.swift', delegate]] as const) {
    const stripped = code(source);
    for (const [open, close] of [['{', '}'], ['(', ')'], ['[', ']']]) {
      assert.equal(stripped.split(open).length, stripped.split(close).length, `${name} ${open}${close}`);
    }
  }
});

test('a background relaunch holds the completion handler before the session exists', () => {
  const launch = section(delegate, 'didFinishLaunchingWithOptions', 'return true');
  // Creating the owner creates the session, so launch must not: on a relaunch for download events
  // the handler has to be held first.
  assert.doesNotMatch(code(launch), /PorticoBackgroundEvents|PorticoDownloadOwner/);
  const wake = section(delegate, 'handleEventsForBackgroundURLSession identifier', '\n  }\n');
  assert.match(code(wake), /PorticoBackgroundEvents\.wake\(identifier, completionHandler\)/);
  const events = section(delegate, '@objc(PorticoBackgroundEvents)', '\n}\n');
  const hold = code(section(events, 'static func wake', '\n  }\n'));
  const stored = hold.indexOf('completions[identifier] = completion');
  assert.ok(stored >= 0, 'the handler is stored');
  assert.ok(stored < hold.indexOf('PorticoDownloadOwner.start()'), 'and only then is the owner (and session) created');
  assert.match(hold, /identifier == PorticoDownloadOwner\.sessionIdentifier/, 'only the downloads session is held');
  assert.match(hold, /dispatchPrecondition\(condition: \.onQueue\(\.main\)\)/);
  // A foreground launch, or a return to the app, starts the owner from the scene.
  const scene = section(delegate, 'class SceneDelegate', '\n}\n');
  assert.match(code(section(scene, 'func sceneWillEnterForeground', '\n  }\n')), /PorticoBackgroundEvents\.startDownloads\(\)/);
  assert.match(code(section(events, 'static func startDownloads', '\n  }\n')), /PorticoDownloadOwner\.start\(\)/);
  assert.match(bridging, /#import "PorticoDownloadOwner\.h"/);
  assert.match(ownerHeader, /\+ \(void\)start;/);
  assert.match(ownerHeader, /@property \(class, nonatomic, readonly\) NSString \*sessionIdentifier;/);
  assert.doesNotMatch(code(section(delegate, 'func connect(_ scene', '\n  }\n')), /Downloads/);
});

test('each completion handler is called once, on the main queue, with no matching by time', () => {
  const events = section(delegate, '@objc(PorticoBackgroundEvents)', '\n}\n');
  const body = code(events);
  assert.match(body, /private static var completions/);
  assert.doesNotMatch(body, /finishedEarly|Date\(\)|timeIntervalSince/);
  assert.doesNotMatch(body, /completions\[identifier\]\?\(\)/);
  const finish = code(section(delegate, '@objc static func finish', '\n  }\n'));
  assert.match(finish, /DispatchQueue\.main\.async \{ completions\.removeValue\(forKey: identifier\)\?\(\) \}/);
  const hold = code(section(events, 'static func wake', '\n  }\n'));
  assert.ok(hold.indexOf('completions.removeValue(forKey: identifier)?()') < hold.indexOf('completions[identifier] = completion'), 'a replaced handler is released first');
});

test('every native wake retries a manifest that could not be read', () => {
  const owner = section(downloads, '@implementation PorticoDownloadOwner', '\n@end\n');
  const start = code(method(owner, '+ (void)start {'));
  assert.match(start, /\[self shared\]/);
  assert.match(start, /isProtectedDataAvailable\) owner\.protectedDataAvailable = YES/);
  assert.match(start, /\[owner retryLoad\]/);
  assert.match(code(method(owner, '- (void)retryLoad {')), /if \(!self->_loaded\) \[self loadManifest\]/);
  // Waiting work, including each finished transfer, tries the read first.
  const when = code(method(owner, '- (void)whenLoaded:(dispatch_block_t)work otherwise:(dispatch_block_t)refused {'));
  assert.ok(when.indexOf('if (!_loaded) [self loadManifest];') < when.indexOf('if (_loaded) { work(); return; }'));
  // So does the end of a wake's events, before the system is told.
  const done = code(method(owner, '- (void)URLSessionDidFinishEventsForBackgroundURLSession:(NSURLSession *)session {'));
  assert.ok(done.indexOf('if (!self->_loaded) [self loadManifest];') >= 0);
  assert.ok(done.indexOf('loadManifest') < done.indexOf('NSSelectorFromString(""'));
  const init = code(method(owner, '- (instancetype)init {'));
  assert.match(init, /UIApplicationProtectedDataDidBecomeAvailable[\s\S]*weakSelf\.protectedDataAvailable = YES;[\s\S]*\[weakSelf retryLoad\]/);
  // React Native starting is one more wake, read on the main queue.
  const module = section(downloads, '@implementation PorticoDownloads {', '\n@end\n');
  assert.match(module, /dispatch_async\(dispatch_get_main_queue\(\), \^\{ \[PorticoDownloadOwner start\]; \}\)/);
});

test('a manifest that stays unreadable after unlock refuses JavaScript instead of hanging', () => {
  const owner = section(downloads, '@implementation PorticoDownloadOwner', '\n@end\n');
  const load = code(method(owner, '- (void)loadManifest {'));
  assert.match(load, /if \(self\.protectedDataAvailable\) \[self refusePending\];\s*return;/);
  assert.match(load, /_unreadable = NO;/);
  const refuse = code(method(owner, '- (void)refusePending {'));
  assert.match(refuse, /_unreadable = YES;/);
  // Work without a refusal (the system's events) stays queued for a later wake.
  assert.match(refuse, /item\[1\] == NSNull\.null\) \[kept addObject:item\]/);
  const when = code(method(owner, '- (void)whenLoaded:(dispatch_block_t)work otherwise:(dispatch_block_t)refused {'));
  assert.match(when, /if \(_unreadable && refused\) \{ refused\(\); return; \}/);
});

test('the session belongs to the owner, under the identifier the system already knows', () => {
  assert.match(downloads, /PorticoDownloadsSession = @"tv\.getportico\.portico\.downloads"/);
  assert.match(downloads, /@interface PorticoDownloads : RCTEventEmitter <RCTBridgeModule>\n/);
  assert.match(downloads, /@interface PorticoDownloadOwner \(\) <NSURLSessionDownloadDelegate>/);
  const owner = section(downloads, '@implementation PorticoDownloadOwner', '\n@end\n');
  assert.match(owner, /backgroundSessionConfigurationWithIdentifier:PorticoDownloadsSession/);
  assert.match(owner, /sessionWithConfiguration:configuration delegate:self/);
  assert.match(owner, /dispatch_once/);
  const module = section(downloads, '@implementation PorticoDownloads {', '\n@end\n');
  assert.doesNotMatch(module, /NSURLSession/);
  assert.match(module, /_owner = \[PorticoDownloadOwner shared\]/);
  assert.match(module, /_owner\.emitter = self/);
  assert.match(module, /methodQueue \{ return _owner\.queue; \}/);
});

test('every JavaScript call is relayed to an owner method of the same name, gated on the manifest only where it is needed', () => {
  const module = section(downloads, '@implementation PorticoDownloads {', '\n@end\n');
  const exported = [...module.matchAll(/^RCT_EXPORT_METHOD\((.*)\) \{\n(.*)\n\}/gm)];
  assert.deepEqual(exported.map(([, signature]) => signature.slice(0, signature.indexOf(':'))), [
    'list', 'start', 'resume', 'pause', 'remove', 'removeGeneration', 'setMeta', 'setMetas',
    'publicationStateOpen', 'publicationStateRead', 'publicationStateWrite',
    'writeExtra', 'readExtra', 'fetchExtra', 'storage', 'setCellular', 'readState', 'writeState', 'localFile', 'publicationOpen',
  ]);
  const owner = section(downloads, '@implementation PorticoDownloadOwner', '\n@end\n');
  const extension = section(downloads, '@interface PorticoDownloadOwner ()', '\n@end\n');
  const independent = new Set(['setCellular:', 'readState:', 'writeState:']);
  for (const [, signature, body] of exported) {
    const selector = signature.replace(/:\([^)]*\)\s*\w+/g, ':').replace(/\s+/g, '');
    const call = signature.replace(/:\([^)]*\)\s*(\w+)/g, ':$1');
    const name = selector.slice(0, selector.indexOf(':') + 1);
    let ownerSignature = signature, ownerCall = call;
    if (name === 'removeGeneration:') {
      ownerSignature = signature.replace('removeGeneration:', 'remove:');
      ownerCall = call.replace('removeGeneration:', 'remove:');
    } else if (name.startsWith('publicationState')) {
      // The bridge lends only its own registry to the actual owner; it cannot
      // dispatch state work against another bridge's publication tickets.
      ownerSignature = signature.replace(`${name}(NSString *)handle`, `${name}(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle`);
      ownerCall = call.replace(`${name}handle`, `${name}self->_publicationState handle:handle`);
    } else if (name === 'publicationOpen:') {
      ownerSignature = signature.replace(' resolve:', ' reads:(PorticoPublicationReads *)reads resolve:');
      ownerCall = call.replace(' resolve:', ' reads:self->_publicationReads resolve:');
    }
    const expected = independent.has(name)
      ? `[_owner ${ownerCall}];`
      : `[_owner whenLoaded:^{ [self->_owner ${ownerCall}]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];`;
    if (ownerSignature === signature) assert.equal(body.trim(), expected, selector);
    else assert.equal(body.replace(/\s/g, ''), expected.replace(/\s/g, ''), selector);
    const implementation = name === 'publicationStateOpen:'
      ? ownerSignature.replace('generation:(NSString *)generation', 'generation:(NSString *)expectedGeneration') : ownerSignature;
    assert.ok(owner.includes(`- (void)${implementation} {`), `owner implements ${selector}`);
    assert.ok(extension.includes(`- (void)${ownerSignature};`), `owner declares ${selector}`);
  }
  assert.doesNotMatch(owner, /RCT_EXPORT/);
  // Cancelling for Wi-Fi only still records each transfer once the manifest is read.
  assert.match(method(owner, '- (void)setCellular:(BOOL)allowed resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {'), /dispatch_async\(self->_queue, \^\{ \[self whenLoaded:\^\{/);
});

test('callbacks wait for the manifest, and an unread manifest is never written over', () => {
  const owner = section(downloads, '@implementation PorticoDownloadOwner', '\n@end\n');
  assert.match(method(owner, '- (BOOL)saveManifest {'), /if \(!_loaded\) return NO;/);
  const load = method(owner, '- (void)loadManifest {');
  // Every marker must be present: indexOf's -1 would otherwise satisfy each ordering (N01). What
  // the manifest restores is executed in apple-download-manifest-tests.m.
  const at = (marker: string) => {const index = load.indexOf(marker); assert.ok(index >= 0, marker); return index;};
  assert.ok(at('if (!entries) { [self refusePending]; return; }') < at('_loaded = YES'), 'an unreadable manifest leaves the owner unloaded');
  assert.ok(at('PorticoDownloadManifestEntries(raw)') < at('_entries = entries'), 'the owner restores exactly what the manifest decodes');
  assert.ok(at('_entries = entries') < at('for (NSArray *item in waiting) ((dispatch_block_t)item[0])();'), 'waiting work runs in arrival order after the read');
  assert.match(owner, /UIApplicationProtectedDataDidBecomeAvailable/);
  // Filing waits for the manifest; a moment's progress before it is read is let go.
  for (const [callback, gate] of [['didWriteData:', 'ifLoaded'], ['taskIsWaitingForConnectivity:', 'ifLoaded'], ['didFinishDownloadingToURL:', 'whenLoaded'], ['didCompleteWithError:', 'whenLoaded']]) {
    const body = section(owner, callback, '\n}\n');
    assert.match(body, new RegExp(`dispatch_async\\(_queue, \\^\\{ \\[self ${gate}:\\^\\{`), callback);
    assert.match(body, /\}\]; \}\);$/, callback);
  }
  // The temporary file is moved into the download's folder before the callback returns, loaded or not.
  const arrived = section(owner, 'didFinishDownloadingToURL:', '\n}\n');
  assert.ok(arrived.indexOf('moveItemAtURL:location') < arrived.indexOf('whenLoaded'));
  assert.match(method(owner, '- (void)reconcile {'), /whenLoaded:\^\{ \[self reconcileNow\]; \}/);
});

test('the system is told the wake is over only after the queued filing, and filing asks for time', () => {
  const owner = section(downloads, '@implementation PorticoDownloadOwner', '\n@end\n');
  const done = method(owner, '- (void)URLSessionDidFinishEventsForBackgroundURLSession:(NSURLSession *)session {');
  assert.match(done, /dispatch_async\(_queue, \^\{/);
  assert.match(done, /NSSelectorFromString\(@"finish:"\)/);
  // Filing is synchronous on the serial queue, so `finish:` follows every write queued before it.
  assert.doesNotMatch(code(method(owner, '- (void)file:(NSString *)identifier from:(NSURL *)holding {')), /dispatch_async/);
  const finish = method(owner, '- (void)finish:(NSString *)identifier from:(NSURL *)holding {');
  assert.match(finish, /beginBackgroundTaskWithName/);
  assert.match(finish, /endBackgroundTask/);
  assert.match(finish, /\[self file:identifier from:holding\];/);
});
