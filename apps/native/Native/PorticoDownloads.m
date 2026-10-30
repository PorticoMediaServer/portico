#import "PorticoPublicationReads.h"
#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>
#import <React/RCTUtils.h>
#import <AVFoundation/AVFoundation.h>
#import <UIKit/UIKit.h>
#import <CommonCrypto/CommonDigest.h>
#import "PorticoDownloadAttempt.h"
#import "PorticoDownloadOwner.h"
#import "PorticoDownloadManifest.h"
#import "PorticoServerTrust.h"
#import "PorticoDownloadStorage.h"

// Offline downloads for iPhone and iPad.
//
// The server prepares a file and hands out an address for it; this module moves the bytes and
// keeps them. Transfers run in a background URLSession, so they continue while the app is
// suspended and are picked up again when it next launches. A finished file is checked against
// the server's SHA-256 before it is called ready: a truncated or substituted file is deleted,
// never played. Files live in Application Support, excluded from iCloud backup (they are
// re-downloadable and can be tens of gigabytes).
//
// Continuing a transfer is the system's work here: a background download task that loses its
// connection, waits for Wi-Fi or is suspended asks for the same address again with a byte range
// by itself, and a task that is cancelled leaves resume data that a new task continues from.
// Both need the address to still be good, which is why the app keeps one address per download
// alive (the server extends it on request) instead of taking a new one for every attempt. The
// resume data is used whenever the download is still of the same file. When the address had to
// change, the address inside the resume data is changed to the new one
// (`PorticoDownloadRetargetResume`); only resume data in a form that cannot be changed, or that
// the system refuses, makes the transfer begin again.
//
// Each entry says what it is doing in three fields. `state` is `downloading`, `paused`, `failed`
// or `ready`. `waiting` says why a download that should be moving is not: `wifi` or `network`.
// `intent` is `hold` once the person has paused it, and `run` otherwise: an entry that is paused
// or failed with `run` stopped by itself and is started again by the app when it can be.
//
// This module knows nothing about accounts, receipts or what may be played; that is decided in
// JavaScript from the server's signed receipt. It moves, stores and verifies files, and hands a finished one to Portico's own player.
//
// BETA06: the session, the manifest and the filing of finished files belong to the app, not to
// React Native. When the system ends a suspended Portico and later relaunches it only to deliver
// download events, no window scene connects and React Native never starts, so the owner below is
// started natively: by the background-session callback, after it has stored the system's
// completion handler (Apple: store the handler, then create the session with the same
// identifier), and by the scene coming to the foreground. Creating the owner creates the session,
// so on a background relaunch no session exists, and no event can be delivered, before the
// handler is held. The owner verifies and files what arrived, writes the manifest, and only then
// lets the system suspend the app again. The `PorticoDownloads` module attaches to that owner
// when (and if) React Native starts, and only relays its calls and events.
//
// Every wake (the background-session callback, the foreground, protected data becoming
// available, each finished transfer and the end of a wake's events) tries again to read a
// manifest that could not be read yet: a notification posted while the app was suspended is not
// delivered later.
//
// A person swiping Portico away is not the same thing: the system then cancels the transfers and
// does not relaunch the app for them. Their tasks report at the next ordinary launch, cancelled
// with resume data, and become paused but still meant to run, so the app continues them.

static NSString *const PorticoDownloadsSession = @"tv.getportico.portico.downloads";
static NSString *const PorticoDownloadChanged = @"PorticoDownloadChanged";

@interface PorticoDownloads : RCTEventEmitter <RCTBridgeModule>
@property (atomic, readonly) BOOL porticoObserving;
@end

// State capabilities belong to the serial download owner, never the manifest or guest.
@interface PorticoPublicationGeneration : NSObject
@property(nonatomic,strong) NSMutableDictionary *entry;
@property(nonatomic,copy) NSString *value;
@property BOOL retired;
@end
@implementation PorticoPublicationGeneration
@end
@interface PorticoPublicationStateTicket : NSObject
@property(nonatomic,copy) NSString *identifier;
@property(nonatomic,strong) PorticoPublicationGeneration *generation;
@property(nonatomic,copy) NSArray<NSString *> *fields;
@end
@implementation PorticoPublicationStateTicket
@end
@interface PorticoPublicationStateRegistry : NSObject
@property(nonatomic,strong) NSMutableDictionary<NSString *,PorticoPublicationStateTicket *> *tickets;
@property(nonatomic,strong) NSMutableSet<NSString *> *used;
@property BOOL closed;
@end
@implementation PorticoPublicationStateRegistry
- (instancetype)init {if((self=[super init])){_tickets=[NSMutableDictionary new];_used=[NSMutableSet new];}return self;}
@end

@interface PorticoDownloadOwner () <NSURLSessionDownloadDelegate>
@property (atomic, weak) PorticoDownloads *emitter;
@property (nonatomic, readonly) dispatch_queue_t queue;
// Set on the main queue once the device has been unlocked since it started (it then stays so:
// the manifest is protected until first unlock only).
@property (atomic) BOOL protectedDataAvailable;
+ (instancetype)shared;
- (void)whenLoaded:(dispatch_block_t)work;
- (void)whenLoaded:(dispatch_block_t)work otherwise:(dispatch_block_t)refused;
- (void)retryLoad;
// What JavaScript may ask, relayed by the module below.
- (void)list:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)start:(NSDictionary *)request resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)resume:(NSDictionary *)request resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)pause:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)remove:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)remove:(NSString *)identifier generation:(NSString *)generation resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)setMeta:(NSString *)identifier meta:(NSString *)meta resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)setMetas:(NSDictionary *)metas resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)writeExtra:(NSString *)identifier name:(NSString *)name text:(NSString *)text resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)readExtra:(NSString *)identifier name:(NSString *)name resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)fetchExtra:(NSString *)identifier name:(NSString *)name url:(NSString *)address resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)storage:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)setCellular:(BOOL)allowed resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)readState:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)writeState:(NSString *)json resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)localFile:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)publicationOpen:(NSString *)handle identifier:(NSString *)identifier owner:(NSString *)owner digest:(NSString *)digest reads:(PorticoPublicationReads *)reads resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)publicationStateOpen:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle identifier:(NSString *)identifier generation:(NSString *)generation expected:(NSString *)expected resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)publicationStateRead:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle name:(NSString *)name resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)publicationStateWrite:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle name:(NSString *)name json:(NSString *)json resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject;
- (void)publicationStateClose:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle;
- (void)publicationStateInvalidate:(PorticoPublicationStateRegistry *)registry;
- (void)retirePublicationGeneration:(NSString *)identifier;
- (PorticoPublicationGeneration *)publicationGeneration:(NSString *)identifier;
@end

@implementation PorticoDownloadOwner {
  NSURLSession *_session;
  NSMutableDictionary<NSString *, NSMutableDictionary *> *_entries;
  NSMutableDictionary<NSString *, PorticoPublicationGeneration *> *_publicationGenerations;
  NSMutableDictionary<NSString *, NSNumber *> *_lastEmit;
  NSMutableDictionary<NSString *, NSURLSessionDownloadTask *> *_activeTasks;
  dispatch_queue_t _queue;
  BOOL _cellular;
  // Whether the manifest on disk has been read. Before the first unlock after a restart it
  // cannot be: nothing may then be decided from, or written over, an empty list.
  BOOL _loaded;
  // The manifest exists and could not be read although the device has been unlocked: calls from
  // JavaScript are refused instead of waiting for it. Every wake still tries again.
  BOOL _unreadable;
  // Independent read admission for JS's settings/outbox document.
  BOOL _stateReadable;
  // Work waiting for the manifest, in arrival order: each is @[work, refused or NSNull].
  NSMutableArray<NSArray *> *_pending;
  id _protectedDataObserver;
}

+ (instancetype)shared {
  static PorticoDownloadOwner *owner; static dispatch_once_t once;
  dispatch_once(&once, ^{ owner = [PorticoDownloadOwner new]; });
  return owner;
}
+ (NSString *)sessionIdentifier { return PorticoDownloadsSession; }
// On the main queue, at every native wake. Creating the owner creates the session.
+ (void)start {
  PorticoDownloadOwner *owner = [self shared];
  if (UIApplication.sharedApplication.isProtectedDataAvailable) owner.protectedDataAvailable = YES;
  [owner retryLoad];
}

- (instancetype)init {
  if ((self = [super init])) {
    _queue = dispatch_queue_create("tv.getportico.downloads", DISPATCH_QUEUE_SERIAL);
    _publicationGenerations = [NSMutableDictionary new];
    _lastEmit = [NSMutableDictionary new];
    _activeTasks = [NSMutableDictionary new];
    _pending = [NSMutableArray new];
    _cellular = [[NSUserDefaults standardUserDefaults] boolForKey:@"PorticoDownloadsCellular"];
    // The manifest is read on the downloads queue, ahead of every delegate callback (which all
    // queue behind it), and the session is created now, while the system is waiting for it.
    dispatch_async(_queue, ^{ [self loadManifest]; });
    __weak PorticoDownloadOwner *weakSelf = self;
    _protectedDataObserver = [[NSNotificationCenter defaultCenter] addObserverForName:UIApplicationProtectedDataDidBecomeAvailable object:nil queue:nil usingBlock:^(NSNotification *note) {
      weakSelf.protectedDataAvailable = YES;
      [weakSelf retryLoad];
    }];
    [self session];
    [self reconcile];
  }
  return self;
}

// Runs `work` on the downloads queue once the manifest has been read, trying to read it first if
// it has not been yet. Call on the downloads queue. Work with no `refused` waits for as long as
// the manifest cannot be read (a later wake may read it); work with one is refused instead once
// the manifest is known to be unreadable rather than still locked.
- (void)whenLoaded:(dispatch_block_t)work { [self whenLoaded:work otherwise:nil]; }
- (void)whenLoaded:(dispatch_block_t)work otherwise:(dispatch_block_t)refused {
  if (!_loaded) [self loadManifest];
  if (_loaded) { work(); return; }
  if (_unreadable && refused) { refused(); return; }
  // Copied: a block literal passed in may still be on the caller's stack.
  [_pending addObject:@[[work copy], refused ? [refused copy] : (id)NSNull.null]];
}
// Progress and waiting reports describe a moment; one that arrives before the manifest has been
// read is let go rather than queued, so the queue stays bounded by the transfers themselves.
- (void)ifLoaded:(dispatch_block_t)work {
  if (_loaded) work();
}
- (void)retryLoad {
  dispatch_async(_queue, ^{ if (!self->_loaded) [self loadManifest]; });
}

#pragma mark - Storage

- (NSURL *)root {
  NSURL *base = [[NSFileManager defaultManager] URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject;
  NSURL *root = [base URLByAppendingPathComponent:@"Downloads" isDirectory:YES];
  [[NSFileManager defaultManager] createDirectoryAtURL:root withIntermediateDirectories:YES attributes:nil error:nil];
  [root setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
  return root;
}
- (NSURL *)manifestURL { return [[self root] URLByAppendingPathComponent:@"manifest.json"]; }
- (NSURL *)stateURL { return [[self root] URLByAppendingPathComponent:@"state.json"]; }
- (NSURL *)directoryFor:(NSString *)identifier { return [[self root] URLByAppendingPathComponent:identifier isDirectory:YES]; }
- (NSURL *)resumeURLFor:(NSString *)identifier { return [[self directoryFor:identifier] URLByAppendingPathComponent:@"resume.data"]; }
// A finished transfer waits here while it is verified. It is inside the download's own folder,
// so a launch that finds one knows whose it is and finishes the job. Each attempt has its own
// name, so a late completion of an attempt that was replaced can never land on top of the file
// the current attempt is verifying.
- (NSURL *)incomingURLFor:(NSString *)identifier task:(NSUInteger)task {
  return [[self directoryFor:identifier] URLByAppendingPathComponent:[NSString stringWithFormat:@"incoming-%lu.part", (unsigned long)task]];
}
- (NSArray<NSURL *> *)incomingFilesFor:(NSString *)identifier {
  NSMutableArray<NSURL *> *found = [NSMutableArray new];
  for (NSURL *file in [[NSFileManager defaultManager] contentsOfDirectoryAtURL:[self directoryFor:identifier] includingPropertiesForKeys:nil options:0 error:nil])
    if ([file.lastPathComponent hasPrefix:@"incoming-"] && [file.pathExtension isEqualToString:@"part"]) [found addObject:file];
  return found;
}
- (NSURL *)extrasFor:(NSString *)identifier { return [[self directoryFor:identifier] URLByAppendingPathComponent:@"extras" isDirectory:YES]; }

// The most one extra (a poster, a subtitle track, the description) may weigh.
static const long long PorticoMaxExtraBytes = 32LL * 1024 * 1024;
// A name for a file kept beside a download: plain characters and at most one short extension.
static BOOL PorticoSafeExtraName(NSString *value) {
  if (![value isKindOfClass:NSString.class] || value.length < 1 || value.length > 106) return NO;
  static NSRegularExpression *pattern; static dispatch_once_t once;
  dispatch_once(&once, ^{ pattern = [NSRegularExpression regularExpressionWithPattern:@"^[A-Za-z0-9_-]{1,96}(\\.[a-z0-9]{1,8})?$" options:0 error:nil]; });
  return [pattern numberOfMatchesInString:value options:0 range:NSMakeRange(0, value.length)] == 1;
}


// An inaccessible path is not evidence that its record is absent.
static BOOL PorticoDownloadMissingFile(NSError *error) {
  return [error.domain isEqual:NSCocoaErrorDomain] && error.code == NSFileReadNoSuchFileError;
}

// A file name from the server is used as a path component, so it is reduced to its last
// component and stripped of anything that could leave the download's own folder.
static NSString *PorticoSafeFileName(NSString *value) {
  NSString *name = [value isKindOfClass:NSString.class] ? value.lastPathComponent : @"";
  name = [[name componentsSeparatedByCharactersInSet:[NSCharacterSet characterSetWithCharactersInString:@"/\\:\0"]] componentsJoinedByString:@"_"];
  if (name.length == 0 || [name hasPrefix:@"."] || name.length > 200) return @"media";
  return name;
}

// On the downloads queue. A manifest that exists but cannot be read leaves the owner unloaded:
// work waits in `_pending`, nothing is written, and the read is tried again at the next wake.
// Before the first unlock that is all. After it, the read is not going to succeed by waiting, so
// what JavaScript asked is refused instead of left hanging; the manifest is still never written
// over, and the system's events stay queued for a later wake that can read it.
- (void)loadManifest {
  _loaded = NO;
  NSError *error = nil;
  NSData *raw = [NSData dataWithContentsOfURL:[self manifestURL] options:0 error:&error];
  if (!raw && !PorticoDownloadMissingFile(error)) {
    if (self.protectedDataAvailable) [self refusePending];
    return;
  }
  NSMutableDictionary<NSString *, NSMutableDictionary *> *entries = PorticoDownloadManifestEntries(raw);
  if (!entries) { [self refusePending]; return; }
  // An extra that was being filed when the app stopped leaves its holding file behind.
  for (NSURL *file in [[NSFileManager defaultManager] contentsOfDirectoryAtURL:[self root] includingPropertiesForKeys:nil options:0 error:nil])
    if ([file.pathExtension isEqualToString:@"extra"]) [[NSFileManager defaultManager] removeItemAtURL:file error:nil];
  for (PorticoPublicationGeneration *generation in _publicationGenerations.allValues) generation.retired = YES;
  [_publicationGenerations removeAllObjects];
  _entries = entries;
  _loaded = YES;
  _unreadable = NO;
  // Callbacks that arrived first are settled in the order they arrived.
  NSArray<NSArray *> *waiting = [_pending copy];
  [_pending removeAllObjects];
  for (NSArray *item in waiting) ((dispatch_block_t)item[0])();
}
- (void)refusePending {
  _unreadable = YES;
  NSMutableArray<NSArray *> *kept = [NSMutableArray new];
  NSMutableArray<dispatch_block_t> *refused = [NSMutableArray new];
  for (NSArray *item in _pending) {
    if (item[1] == NSNull.null) [kept addObject:item]; else [refused addObject:item[1]];
  }
  [_pending setArray:kept];
  for (dispatch_block_t refuse in refused) refuse();
}

// What the last run left unfinished is settled once at launch.
//
// A transfer that had fully arrived and was being verified when the app was stopped still has its
// bytes in the download's folder: it is verified now, instead of being thrown away and fetched
// again. An entry that says it is downloading and has no task behind it (the task was lost with
// the app) becomes paused, still meant to run, so the app starts it again; left as it was it
// would show a progress bar that never moves and offer only Pause.
//
// It waits a few seconds first. When the system relaunches the app because a transfer finished
// while it was away, that transfer's completion is delivered just after the session is created;
// looking sooner would find an entry with no running task and call it paused a moment before its
// finished file arrives.
- (void)reconcile {
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(5 * NSEC_PER_SEC)), _queue, ^{ [self whenLoaded:^{ [self reconcileNow]; }]; });
}
- (void)reconcileNow {
  [[self session] getTasksWithCompletionHandler:^(NSArray *data, NSArray *uploads, NSArray<NSURLSessionDownloadTask *> *downloads) {
    dispatch_async(self->_queue, ^{
      NSMutableSet<NSString *> *live = [NSMutableSet new];
      for (NSURLSessionDownloadTask *task in downloads) {
        NSDictionary *entry = self->_entries[task.taskDescription ?: @""];
        // A task the session still lists, in any state, will report for itself.
        if (PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) [live addObject:task.taskDescription];
        else [task cancel];
      }
      BOOL changed = NO;
      for (NSString *identifier in self->_entries.allKeys) {
        NSMutableDictionary *entry = self->_entries[identifier];
        NSArray<NSURL *> *arrived = [self incomingFilesFor:identifier];
        if ([entry[@"state"] isEqual:@"ready"] || [live containsObject:identifier]) {
          // Nothing is waiting to be verified for a finished download, and a download with a
          // task behind it will deliver its own file.
          if ([entry[@"state"] isEqual:@"ready"]) for (NSURL *file in arrived) [[NSFileManager defaultManager] removeItemAtURL:file error:nil];
          continue;
        }
        if (arrived.count) {
          for (NSUInteger i = 1; i < arrived.count; i++) [[NSFileManager defaultManager] removeItemAtURL:arrived[i] error:nil];
          [self finish:identifier from:arrived[0]];
          continue;
        }
        if ([entry[@"state"] isEqual:@"downloading"] && ![live containsObject:identifier]) {
          entry[@"state"] = @"paused"; entry[@"error"] = @""; entry[@"waiting"] = @"";
          changed = YES;
          [self changed:identifier force:YES];
        }
      }
      if (changed) [self saveManifest];
    });
  }];
}

// An entry as JavaScript sees it: what is stored, plus where its folder is in this launch (the
// app's container moves between installs, so a path is never stored). The address is a
// credential: it is used here and never handed back.
- (NSDictionary *)published:(NSString *)identifier {
  NSMutableDictionary *entry = [self->_entries[identifier] mutableCopy];
  if (!entry) return @{@"id": identifier, @"state": @"removed"};
  entry[@"dir"] = [self directoryFor:identifier].absoluteString;
  entry[@"publicationGeneration"] = [self publicationGeneration:identifier].value;
  [entry removeObjectForKey:@"grantUrl"];
  [entry removeObjectForKey:@"retargeted"];
  [entry removeObjectForKey:@"resumed"];
  return entry;
}
- (BOOL)saveManifest {
  // An unread manifest is never replaced by what little this launch knows.
  if (!_loaded) return NO;
  NSData *raw = [NSJSONSerialization dataWithJSONObject:_entries options:0 error:nil];
  // SEC-22: the manifest names titles and, while a transfer runs, its grant address; it is only
  // readable once the device has been unlocked after boot (background sessions still work).
  return raw && [raw writeToURL:[self manifestURL] options:NSDataWritingAtomic | NSDataWritingFileProtectionCompleteUntilFirstUserAuthentication error:nil];
}
- (void)changed:(NSString *)identifier force:(BOOL)force {
  // Progress arrives many times a second; the interface needs it about twice a second.
  NSTimeInterval now = NSDate.date.timeIntervalSince1970;
  if (!force && now - _lastEmit[identifier].doubleValue < 0.5) return;
  _lastEmit[identifier] = @(now);
  // Without React Native (a background relaunch) the manifest is the record; the app reads it
  // when it next lists its downloads.
  PorticoDownloads *emitter = self.emitter;
  if (emitter.porticoObserving) [emitter sendEventWithName:PorticoDownloadChanged body:[self published:identifier]];
}

#pragma mark - Session

- (NSURLSession *)session {
  if (_session) return _session;
  NSURLSessionConfiguration *configuration = [NSURLSessionConfiguration backgroundSessionConfigurationWithIdentifier:PorticoDownloadsSession];
  configuration.sessionSendsLaunchEvents = YES;
  configuration.discretionary = NO;
  // A background session's configuration is fixed once it is created, so baking
  // the current preference in here meant a later change did nothing until the
  // next launch. The session is therefore permissive and every request tightens
  // itself to the live policy — a request can restrict what the session allows,
  // it just cannot widen it. See -requestFor:.
  configuration.allowsCellularAccess = YES;
  configuration.allowsExpensiveNetworkAccess = YES;
  configuration.HTTPMaximumConnectionsPerHost = 2;
  _session = [NSURLSession sessionWithConfiguration:configuration delegate:self delegateQueue:nil];
  return _session;
}

/// A request carrying the network policy in force right now.
- (NSURLRequest *)requestFor:(NSURL *)url {
  NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];
  request.allowsCellularAccess = _cellular;
  request.allowsExpensiveNetworkAccess = _cellular;
  return request;
}

- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {[PorticoServerTrust handleChallenge:challenge completion:completionHandler];}
- (void)URLSession:(NSURLSession *)session downloadTask:(NSURLSessionDownloadTask *)task didWriteData:(int64_t)written totalBytesWritten:(int64_t)total totalBytesExpectedToWrite:(int64_t)expected {
  NSString *identifier = task.taskDescription;
  if (!PorticoSafeIdentifier(identifier)) return;
  dispatch_async(_queue, ^{ [self ifLoaded:^{
    NSMutableDictionary *entry = self->_entries[identifier];
    if (!PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) return;
    entry[@"bytesDone"] = @(total);
    if (expected > 0) entry[@"bytes"] = @(expected);
    entry[@"state"] = @"downloading";
    BOOL waited = [entry[@"waiting"] length] > 0;
    entry[@"waiting"] = @"";
    [self changed:identifier force:waited];
  }]; });
}

// The system is holding the task until it has a connection the task may use: no connection at
// all, or mobile data only while "Wi-Fi only" is on. It starts the task by itself when one comes.
- (void)URLSession:(NSURLSession *)session taskIsWaitingForConnectivity:(NSURLSessionTask *)task {
  NSString *identifier = task.taskDescription;
  if (!PorticoSafeIdentifier(identifier)) return;
  dispatch_async(_queue, ^{ [self ifLoaded:^{
    NSMutableDictionary *entry = self->_entries[identifier];
    if (!PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) return;
    entry[@"waiting"] = self->_cellular ? @"network" : @"wifi";
    [self changed:identifier force:YES];
  }]; });
}

- (void)URLSession:(NSURLSession *)session downloadTask:(NSURLSessionDownloadTask *)task didFinishDownloadingToURL:(NSURL *)location {
  NSString *identifier = task.taskDescription;
  if (!PorticoSafeIdentifier(identifier)) return;
  NSInteger status = [task.response isKindOfClass:NSHTTPURLResponse.class] ? ((NSHTTPURLResponse *)task.response).statusCode : 0;
  // The temporary file is only valid during this call: move it before returning. It goes into
  // the download's own folder, so if the app is stopped before it has been verified the next
  // launch finds it there and finishes (see -reconcile).
  BOOL known = PorticoSafeIdentifier(identifier);
  NSURL *holding = known ? [self incomingURLFor:identifier task:task.taskIdentifier] : nil;
  BOOL moved = NO;
  if (known && status >= 200 && status <= 299) {
    [[NSFileManager defaultManager] createDirectoryAtURL:[self directoryFor:identifier] withIntermediateDirectories:YES attributes:nil error:nil];
    [[NSFileManager defaultManager] removeItemAtURL:holding error:nil];
    moved = [[NSFileManager defaultManager] moveItemAtURL:location toURL:holding error:nil];
  }
  dispatch_async(_queue, ^{ [self whenLoaded:^{
    NSMutableDictionary *entry = self->_entries[identifier];
    // An attempt that was replaced, or a download that was removed, has nothing to file.
    if (!PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) {
      if (moved) [[NSFileManager defaultManager] removeItemAtURL:holding error:nil];
      if (moved && !entry) [[NSFileManager defaultManager] removeItemAtURL:[self directoryFor:identifier] error:nil];
      return;
    }
    [self->_activeTasks removeObjectForKey:identifier];
    if (status < 200 || status > 299) {
      // The address has run out, or the server will not hand this over just now. The app renews
      // the address and starts the transfer again; nothing here is the person's to fix.
      entry[@"state"] = @"failed"; entry[@"waiting"] = @"";
      entry[@"error"] = (status == 401 || status == 403 || status == 404 || status == 409 || status == 410) ? @"grant_expired" : (status == 429 || status == 503) ? @"network" : @"server_refused";
      [self saveManifest]; [self changed:identifier force:YES];
      return;
    }
    if (!moved) {
      entry[@"state"] = @"failed"; entry[@"error"] = @"storage_full"; entry[@"waiting"] = @"";
      [self saveManifest]; [self changed:identifier force:YES];
      return;
    }
    [self finish:identifier from:holding];
  }]; });
}

// Verifies the bytes waiting in a download's folder and files them. On the downloads queue.
//
// Checking a large file takes a while, and a background relaunch only lasts as long as the
// system allows, so the work asks for time of its own. If that runs out the app is suspended with
// the bytes still in the download's folder, and the work carries on when it resumes, or the next
// launch's reconcile finishes it.
- (void)finish:(NSString *)identifier from:(NSURL *)holding {
  UIApplication *app = UIApplication.sharedApplication;
  __block UIBackgroundTaskIdentifier time = UIBackgroundTaskInvalid;
  time = [app beginBackgroundTaskWithName:@"PorticoDownloadFiling" expirationHandler:^{
    if (time != UIBackgroundTaskInvalid) { [app endBackgroundTask:time]; time = UIBackgroundTaskInvalid; }
  }];
  [self file:identifier from:holding];
  dispatch_async(dispatch_get_main_queue(), ^{
    if (time != UIBackgroundTaskInvalid) { [app endBackgroundTask:time]; time = UIBackgroundTaskInvalid; }
  });
}
- (void)file:(NSString *)identifier from:(NSURL *)holding {
  NSMutableDictionary *entry = self->_entries[identifier];
  if (!entry || [entry[@"state"] isEqual:@"ready"]) { [[NSFileManager defaultManager] removeItemAtURL:holding error:nil]; return; }
  [self retirePublicationGeneration:identifier];
  NSString *expected = [entry[@"sha256"] isKindOfClass:NSString.class] ? entry[@"sha256"] : @"";
  NSString *actual = expected.length ? [self digestOf:holding] : @"";
  if (expected.length && ![actual isEqualToString:expected.lowercaseString]) {
    [[NSFileManager defaultManager] removeItemAtURL:holding error:nil];
    [[NSFileManager defaultManager] removeItemAtURL:[self resumeURLFor:identifier] error:nil];
    entry[@"state"] = @"failed"; entry[@"error"] = @"verification_failed"; entry[@"waiting"] = @"";
    [self saveManifest]; [self changed:identifier force:YES];
    return;
  }
  NSURL *final = [[self directoryFor:identifier] URLByAppendingPathComponent:PorticoSafeFileName(entry[@"fileName"])];
  [[NSFileManager defaultManager] removeItemAtURL:final error:nil];
  if (![[NSFileManager defaultManager] moveItemAtURL:holding toURL:final error:nil]) {
    [[NSFileManager defaultManager] removeItemAtURL:holding error:nil];
    entry[@"state"] = @"failed"; entry[@"error"] = @"storage_full"; entry[@"waiting"] = @"";
    [self saveManifest]; [self changed:identifier force:YES];
    return;
  }
  [final setResourceValue:@YES forKey:NSURLIsExcludedFromBackupKey error:nil];
  NSFileHandle *durableFile = [NSFileHandle fileHandleForWritingToURL:final error:nil];
  BOOL durable = durableFile && [durableFile synchronizeAndReturnError:nil];
  [durableFile closeAndReturnError:nil];
  if (!durable) {
    // Preserve the verified payload for recovery, without claiming that it is
    // durably available offline before both bytes and record are committed.
    entry[@"state"] = @"failed"; entry[@"error"] = @"transfer_failed"; entry[@"waiting"] = @"";
    [entry removeObjectForKey:@"grantUrl"];
    [self saveManifest]; [self changed:identifier force:YES];
    return;
  }
  [[NSFileManager defaultManager] removeItemAtURL:[self resumeURLFor:identifier] error:nil];
  NSNumber *size = [[NSFileManager defaultManager] attributesOfItemAtPath:final.path error:nil][NSFileSize];
  entry[@"state"] = @"ready"; entry[@"error"] = @""; entry[@"waiting"] = @""; entry[@"bytes"] = size ?: @0; entry[@"bytesDone"] = size ?: @0;
  entry[@"file"] = final.lastPathComponent; entry[@"completedAt"] = @((long long)(NSDate.date.timeIntervalSince1970 * 1000));
  // SEC-22: the address has done its job; a finished download keeps no token.
  [entry removeObjectForKey:@"grantUrl"];
  if (![self saveManifest]) {
    // The verified file stays on disk, but an uncommitted local record is not
    // an offline-ready download and must never produce a completion notice.
    entry[@"state"] = @"failed"; entry[@"error"] = @"transfer_failed";
    [entry removeObjectForKey:@"completedAt"];
    [self saveManifest];
  }
  [self changed:identifier force:YES];
  // No notifications (Justin's rule): completion shows in the app (Downloads state and a toast).
}

/** All events for a background relaunch have been delivered: let iOS suspend the app again. */
- (void)URLSessionDidFinishEventsForBackgroundURLSession:(NSURLSession *)session {
  NSString *identifier = session.configuration.identifier ?: @"";
  dispatch_async(_queue, ^{
    // Verification, filing and manifest writes queued by the delegate calls above finish first
    // (same serial queue), and this wake is one more chance to read a manifest that could not be
    // read yet, which settles what waited for it. If it still cannot be read, the bytes are
    // already in the download's own folder, where a later load settles them, so the system is
    // still told now: holding its completion handler would not make the manifest readable.
    if (!self->_loaded) [self loadManifest];
    Class events = NSClassFromString(@"PorticoBackgroundEvents");
    SEL finish = NSSelectorFromString(@"finish:");
    if ([events respondsToSelector:finish]) { ((void (*)(id, SEL, NSString *))[events methodForSelector:finish])(events, finish, identifier); }
  });
}

- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
  if (!error) return;
  NSString *identifier = task.taskDescription;
  if (!PorticoSafeIdentifier(identifier)) return;
  NSData *resume = error.userInfo[NSURLSessionDownloadTaskResumeData];
  dispatch_async(_queue, ^{ [self whenLoaded:^{
    NSMutableDictionary *entry = self->_entries[identifier];
    if (!PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) return;
    [self->_activeTasks removeObjectForKey:identifier];
    if (resume.length) {
      [[NSFileManager defaultManager] createDirectoryAtURL:[self directoryFor:identifier] withIntermediateDirectories:YES attributes:nil error:nil];
      [resume writeToURL:[self resumeURLFor:identifier] options:NSDataWritingAtomic error:nil];
    } else if ([entry[@"resumed"] boolValue] && !([error.domain isEqual:NSURLErrorDomain] && error.code == NSURLErrorCancelled)) {
      // Any failed resume, including the same grant, must not replay an unusable old blob.
      [[NSFileManager defaultManager] removeItemAtURL:[self resumeURLFor:identifier] error:nil];
    }
    [entry removeObjectForKey:@"retargeted"];
    [entry removeObjectForKey:@"resumed"];
    BOOL cancelled = [error.domain isEqual:NSURLErrorDomain] && error.code == NSURLErrorCancelled;
    entry[@"state"] = cancelled ? @"paused" : @"failed";
    entry[@"waiting"] = @"";
    // Anything the network did is the network's to mend: the app starts the transfer again and
    // the resume data just kept carries it on from the bytes already here.
    BOOL network = [error.domain isEqual:NSURLErrorDomain] && error.code != NSURLErrorCannotWriteToFile && error.code != NSURLErrorCannotCreateFile;
    entry[@"error"] = cancelled ? @"" : (network ? @"network" : @"transfer_failed");
    [self saveManifest]; [self changed:identifier force:YES];
  }]; });
}

- (NSString *)digestOf:(NSURL *)file {
  NSInputStream *stream = [NSInputStream inputStreamWithURL:file];
  [stream open];
  CC_SHA256_CTX context; CC_SHA256_Init(&context);
  uint8_t *buffer = malloc(1 << 20);
  if (!buffer) { [stream close]; return @""; }
  NSInteger read;
  while ((read = [stream read:buffer maxLength:1 << 20]) > 0) CC_SHA256_Update(&context, buffer, (CC_LONG)read);
  free(buffer); [stream close];
  if (read < 0) return @"";
  unsigned char digest[CC_SHA256_DIGEST_LENGTH]; CC_SHA256_Final(digest, &context);
  NSMutableString *hex = [NSMutableString stringWithCapacity:64];
  for (int i = 0; i < CC_SHA256_DIGEST_LENGTH; i++) [hex appendFormat:@"%02x", digest[i]];
  return hex;
}

#pragma mark - JavaScript interface

- (void)list:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    NSMutableArray *all = [NSMutableArray new];
    for (NSString *identifier in self->_entries) [all addObject:[self published:identifier]];
    resolve(all);
  });
}

- (void)start:(NSDictionary *)request resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  NSString *identifier = request[@"id"];
  NSURL *url = [request[@"url"] isKindOfClass:NSString.class] ? [NSURL URLWithString:request[@"url"]] : nil;
  if (!PorticoSafeIdentifier(identifier) || !url || !([url.scheme isEqual:@"https"] || [url.scheme isEqual:@"http"])) { reject(@"invalid_download", @"The download request is invalid.", nil); return; }
  // Continue and retry both arrive here, so an earlier task for the same
  // download is ended first: two tasks writing one file is worse than starting
  // again. Cancelling without resume data is deliberate — the blob below is the
  // one that matters, and it is only kept when its grant is still the current one.
  [self taskFor:identifier then:^(NSURLSessionDownloadTask *existing) {
    [existing cancel];
    [self beginTransfer:request identifier:identifier url:url resolve:resolve];
  }];
}

// Like conditional removal, recheck after task lookup and perform the operation in that
// same owner turn. No queued continuation may recreate an absent or replaced entry.
- (void)resume:(NSDictionary *)request resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  NSString *identifier = request[@"id"];
  NSString *generation = [request[@"expectedGeneration"] isKindOfClass:NSString.class] ? request[@"expectedGeneration"] : nil;
  if (!PorticoSafeIdentifier(identifier) || ![self isGeneration:generation currentFor:identifier]
      || [self->_entries[identifier][@"state"] isEqual:@"removed"]) {
    reject(@"generation_changed", @"This download changed before it could be continued.", nil); return;
  }
  NSURL *url = [request[@"url"] isKindOfClass:NSString.class] ? [NSURL URLWithString:request[@"url"]] : nil;
  if (!url || !([url.scheme isEqual:@"https"] || [url.scheme isEqual:@"http"])) { reject(@"invalid_download", @"The download request is invalid.", nil); return; }
  [self taskFor:identifier then:^(NSURLSessionDownloadTask *existing) {
    if (![self isGeneration:generation currentFor:identifier] || [self->_entries[identifier][@"state"] isEqual:@"removed"]) {
      reject(@"generation_changed", @"This download changed before it could be continued.", nil); return;
    }
    [existing cancel];
    [self beginTransfer:request identifier:identifier url:url resolve:resolve];
  }];
}

// On the owner queue, without another dispatch between a generation check and mutation.
- (void)beginTransfer:(NSDictionary *)request identifier:(NSString *)identifier url:(NSURL *)url resolve:(RCTPromiseResolveBlock)resolve {
  NSMutableDictionary *entry = self->_entries[identifier] ?: [NSMutableDictionary new];
  if ([entry[@"state"] isEqual:@"ready"]) { self->_entries[identifier] = entry; resolve([self published:identifier]); return; }
  [self retirePublicationGeneration:identifier];
  // The bytes already fetched belong to one exact file.
  NSString *wanted = [request[@"sha256"] isKindOfClass:NSString.class] ? [request[@"sha256"] lowercaseString] : @"";
  BOOL sameFile = wanted.length > 0 && [entry[@"sha256"] isKindOfClass:NSString.class] && [[entry[@"sha256"] lowercaseString] isEqual:wanted];
  for (NSString *key in @[@"itemId", @"title", @"subtitle", @"quality", @"fileName", @"sha256", @"contentType", @"meta"]) if ([request[key] isKindOfClass:NSString.class]) entry[key] = request[key];
  entry[@"id"] = identifier;
  entry[@"bytes"] = [request[@"bytes"] isKindOfClass:NSNumber.class] ? request[@"bytes"] : @0;
  entry[@"state"] = @"downloading"; entry[@"error"] = @""; entry[@"waiting"] = @""; entry[@"intent"] = @"run";
  if (!entry[@"addedAt"]) entry[@"addedAt"] = @((long long)(NSDate.date.timeIntervalSince1970 * 1000));
  self->_entries[identifier] = entry;
  NSURL *resumeURL = [self resumeURLFor:identifier];
  NSData *resume = [NSData dataWithContentsOfURL:resumeURL];
  NSString *address = url.absoluteString;
  // Resume data carries its original request: address and mobile-data policy. Either change is
  // written into the saved request so the bytes already here are kept (N12, JUSTIN-ANSWERS #17).
  BOOL samePolicy = PorticoDownloadResumePolicyMatches(entry, self->_cellular);
  BOOL sameGrant = sameFile && samePolicy && resume.length > 0 && [entry[@"grantUrl"] isKindOfClass:NSString.class] && [entry[@"grantUrl"] isEqual:address];
  [entry removeObjectForKey:@"retargeted"];
  if (resume.length > 0 && !sameGrant) {
    NSData *moved = sameFile ? PorticoDownloadRetargetResume(resume, url, self->_cellular) : nil;
    if (moved.length && [moved writeToURL:resumeURL options:NSDataWritingAtomic error:nil]) {
      resume = moved;
      entry[@"retargeted"] = @YES;
      entry[@"cellularAllowed"] = @(self->_cellular);
    } else if (sameFile && !samePolicy && !self->_cellular) {
      // Data in a form this cannot rewrite may allow mobile data: never replay it under
      // "Wi-Fi only", and never erase its partial bytes. It waits, paused, for the setting.
      [self->_activeTasks[identifier] cancel];
      [self->_activeTasks removeObjectForKey:identifier];
      [entry removeObjectForKey:@"taskIdentifier"];
      entry[@"state"] = @"paused"; entry[@"waiting"] = @"wifi";
      [self saveManifest]; [self changed:identifier force:YES];
      resolve([self published:identifier]); return;
    } else if (!(sameFile && !samePolicy && self->_cellular && [entry[@"grantUrl"] isEqual:address])) {
      // A Wi-Fi-only request stays safe after loosening; anything else begins again.
      [[NSFileManager defaultManager] removeItemAtURL:resumeURL error:nil];
      resume = nil;
    }
  }
  if (!resume.length) entry[@"bytesDone"] = @0;
  entry[@"grantUrl"] = address;
  NSURLSessionDownloadTask *task = resume.length ? [[self session] downloadTaskWithResumeData:resume] : nil;
  BOOL resumed = task != nil;
  // Resume data the system will not take (a moved one included) is let go: the transfer begins again.
  if (!task) {
    if (resume.length) { [[NSFileManager defaultManager] removeItemAtURL:resumeURL error:nil]; entry[@"bytesDone"] = @0; [entry removeObjectForKey:@"retargeted"]; }
    task = [[self session] downloadTaskWithRequest:[self requestFor:url]];
    entry[@"cellularAllowed"] = @(self->_cellular);
  }
  entry[@"resumed"] = @(resumed);
  // Serial publication fences every callback from the previous attempt,
  // including a late cancellation that carries the previous grant’s resume data.
  [self->_activeTasks[identifier] cancel];
  entry[@"taskIdentifier"] = @(task.taskIdentifier);
  self->_activeTasks[identifier] = task;
  task.taskDescription = identifier;
  if ([request[@"bytes"] isKindOfClass:NSNumber.class]) task.countOfBytesClientExpectsToReceive = [request[@"bytes"] longLongValue];
  [task resume];
  [self saveManifest]; [self changed:identifier force:YES];
  resolve([self published:identifier]);
}

- (void)taskFor:(NSString *)identifier then:(void (^)(NSURLSessionDownloadTask *))work {
  [[self session] getTasksWithCompletionHandler:^(NSArray *data, NSArray *uploads, NSArray<NSURLSessionDownloadTask *> *downloads) {
    dispatch_async(self->_queue, ^{
      NSDictionary *entry = self->_entries[identifier];
      for (NSURLSessionDownloadTask *task in downloads) {
        if ([task.taskDescription isEqual:identifier] && PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) { work(task); return; }
      }
      work(nil);
    });
  }];
}

- (void)pause:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  NSMutableDictionary *incarnation = self->_entries[identifier];
  [self retirePublicationGeneration:identifier];
  // Record the person's hold before task lookup yields, so policy reconciliation cannot
  // restart this entry while the pause is waiting for the session's task list.
  if (incarnation && ![incarnation[@"state"] isEqual:@"ready"]) {
    incarnation[@"intent"] = @"hold";
    [self saveManifest];
  }
  [self taskFor:identifier then:^(NSURLSessionDownloadTask *task) {
    // The person paused it: it stays paused until they continue it, with or without a task
    // behind it right now.
    NSMutableDictionary *entry = self->_entries[identifier];
    if (entry != incarnation) { reject(@"generation_changed", @"This download changed before it could be paused.", nil); return; }
    if (entry && ![entry[@"state"] isEqual:@"ready"]) {
      entry[@"intent"] = @"hold";
      if (!task && ![entry[@"state"] isEqual:@"failed"]) { entry[@"state"] = @"paused"; entry[@"waiting"] = @""; }
      [self saveManifest]; [self changed:identifier force:YES];
    }
    // The resume data arrives through didCompleteWithError, which records the paused state.
    if (task) [task cancelByProducingResumeData:^(NSData *data) {}];
    resolve(nil);
  }];
}

- (void)remove:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  if (!PorticoSafeIdentifier(identifier)) { reject(@"invalid_download", @"The download is invalid.", nil); return; }
  [self taskFor:identifier then:^(NSURLSessionDownloadTask *task) {
    [task cancel];
    dispatch_async(self->_queue, ^{
      [self->_activeTasks[identifier] cancel];
      [self->_activeTasks removeObjectForKey:identifier];
      [self retirePublicationGeneration:identifier];
      [self->_entries removeObjectForKey:identifier];
      [[NSFileManager defaultManager] removeItemAtURL:[self directoryFor:identifier] error:nil];
      [self saveManifest]; [self changed:identifier force:YES];
      resolve(nil);
    });
  }];
}

/** The exact published entry version this owner queued, still current on its queue. */
- (BOOL)isGeneration:(NSString *)generation currentFor:(NSString *)identifier {
  PorticoPublicationGeneration *live = self->_publicationGenerations[identifier];
  return live != nil && !live.retired && live.entry == self->_entries[identifier]
    && generation != nil && [live.value isEqualToString:generation];
}

- (void)remove:(NSString *)identifier generation:(NSString *)generation resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  if (!PorticoSafeIdentifier(identifier)) { reject(@"invalid_download", @"The download is invalid.", nil); return; }
  dispatch_async(self->_queue, ^{
    // The caller confirmed one exact entry version. It is matched before this asks for a
    // task or cancels anything, so a replaced download is never touched as the later one.
    if (![self isGeneration:generation currentFor:identifier]) { reject(@"generation_changed", @"This download changed before it could be removed.", nil); return; }
    // taskFor only reads session tasks; its callback runs on this queue, where the same
    // version is matched again before the first cancel and stays true through the removal.
    [self taskFor:identifier then:^(NSURLSessionDownloadTask *task) {
      if (![self isGeneration:generation currentFor:identifier]) { reject(@"generation_changed", @"This download changed before it could be removed.", nil); return; }
      [task cancel];
      [self->_activeTasks[identifier] cancel];
      [self->_activeTasks removeObjectForKey:identifier];
      [self retirePublicationGeneration:identifier];
      [self->_entries removeObjectForKey:identifier];
      [[NSFileManager defaultManager] removeItemAtURL:[self directoryFor:identifier] error:nil];
      [self saveManifest]; [self changed:identifier force:YES];
      resolve(nil);
    }];
  });
}

- (void)setMeta:(NSString *)identifier meta:(NSString *)meta resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    // The caller's bookkeeping (a receipt, its owner) is only acknowledged once it is on disk.
    NSMutableDictionary *entry = self->_entries[identifier];
    if (!entry) { reject(@"not_found", @"This download no longer exists.", nil); return; }
    if (meta.length >= 65536) { reject(@"too_large", @"This download's details are too large to keep.", nil); return; }
    id previous = entry[@"meta"];
    if (![previous isEqual:meta]) [self retirePublicationGeneration:identifier];
    entry[@"meta"] = meta;
    if (![self saveManifest]) {
      if (previous) entry[@"meta"] = previous; else [entry removeObjectForKey:@"meta"];
      reject(@"write_failed", @"This download's details could not be saved.", nil); return;
    }
    [self changed:identifier force:YES];
    resolve(nil);
  });
}

// Several entries' details in one write of the manifest (a day's receipt renewals).
- (void)setMetas:(NSDictionary *)metas resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    // Validate the whole batch before changing any entry. A later invalid value must not
    // leave earlier receipts changed in memory and saved by an unrelated progress update.
    for (NSString *identifier in metas) {
      if (!self->_entries[identifier]) continue;
      NSString *meta = metas[identifier];
      if (![meta isKindOfClass:NSString.class] || meta.length >= 65536) { reject(@"too_large", @"This download's details are too large to keep.", nil); return; }
    }
    NSMutableDictionary *previous = [NSMutableDictionary new];
    for (NSString *identifier in metas) {
      NSMutableDictionary *entry = self->_entries[identifier];
      if (!entry) continue;
      previous[identifier] = entry[@"meta"] ?: NSNull.null;
      if (![entry[@"meta"] isEqual:metas[identifier]]) [self retirePublicationGeneration:identifier];
      entry[@"meta"] = metas[identifier];
    }
    if (![self saveManifest]) {
      for (NSString *identifier in previous) {
        if (previous[identifier] == NSNull.null) [self->_entries[identifier] removeObjectForKey:@"meta"]; else self->_entries[identifier][@"meta"] = previous[identifier];
      }
      reject(@"write_failed", @"This download's details could not be saved.", nil); return;
    }
    for (NSString *identifier in previous) [self changed:identifier force:YES];
    resolve(nil);
  });
}

#pragma mark - What travels with a download

- (NSURL *)extraURL:(NSString *)identifier name:(NSString *)name {
  if (!PorticoSafeIdentifier(identifier) || !self->_entries[identifier] || !PorticoSafeExtraName(name)) return nil;
  // Shared by generic read/write/fetch: aliases on case-insensitive storage cannot bypass custody.
  NSString *reserved = name.lowercaseString;
  if ([reserved hasPrefix:@"reader-state-"] || ([reserved isEqual:@"publication.json"] && ![name isEqual:@"publication.json"])) return nil;
  return [[self extrasFor:identifier] URLByAppendingPathComponent:name];
}

- (void)retirePublicationGeneration:(NSString *)identifier {
  _publicationGenerations[identifier].retired = YES;
  [_publicationGenerations removeObjectForKey:identifier];
}
- (PorticoPublicationGeneration *)publicationGeneration:(NSString *)identifier {
  NSMutableDictionary *entry = _entries[identifier];
  PorticoPublicationGeneration *generation = _publicationGenerations[identifier];
  if (generation && generation.entry == entry && !generation.retired) return generation;
  generation = [PorticoPublicationGeneration new]; generation.entry = entry; generation.value = NSUUID.UUID.UUIDString;
  _publicationGenerations[identifier] = generation;
  return generation;
}
// Equality/custody of the original signed envelope; core still owns verification/admission.
static NSArray<NSString *> *PorticoPublicationStateFields(NSDictionary *entry) {
  NSString *raw = entry[@"meta"]; if (![raw isKindOfClass:NSString.class]) return nil;
  NSData *data = [raw dataUsingEncoding:NSUTF8StringEncoding]; if (!data) return nil;
  NSDictionary *meta = [NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
  if (![meta isKindOfClass:NSDictionary.class]) return nil;
  NSDictionary *receipt = meta[@"receipt"]; if (![receipt isKindOfClass:NSDictionary.class]) return nil;
  NSDictionary *claims = receipt[@"claims"]; if (![claims isKindOfClass:NSDictionary.class]) return nil;
  NSDictionary *publication = claims[@"publication"], *artifact = claims[@"artifact"];
  if (![publication isKindOfClass:NSDictionary.class] || ![artifact isKindOfClass:NSDictionary.class]) return nil;
  NSNumber *bytes = entry[@"bytes"], *claimedBytes = artifact[@"bytes"];
  if (![bytes isKindOfClass:NSNumber.class] || ![claimedBytes isKindOfClass:NSNumber.class] || bytes.longLongValue < 1 || bytes.longLongValue > 2147483648LL || bytes.doubleValue != bytes.longLongValue || ![bytes isEqualToNumber:claimedBytes] || ![artifact[@"sha256"] isEqual:entry[@"sha256"]] || ![claims[@"itemId"] isEqual:entry[@"itemId"]]) return nil;
  NSArray *fields = @[entry[@"itemId"] ?: NSNull.null, meta[@"owner"] ?: NSNull.null, entry[@"sha256"] ?: NSNull.null,
    [NSString stringWithFormat:@"%lld",bytes.longLongValue], receipt[@"keyId"] ?: NSNull.null, receipt[@"payload"] ?: NSNull.null, receipt[@"signature"] ?: NSNull.null,
    publication[@"sourceId"] ?: NSNull.null, publication[@"revision"] ?: NSNull.null, publication[@"format"] ?: NSNull.null, publication[@"documentSha256"] ?: NSNull.null];
  for (id field in fields) if (![field isKindOfClass:NSString.class] || ![field length]) return nil;
  return fields;
}
static BOOL PorticoPublicationStateHandle(NSString *handle) {
  if (![handle isKindOfClass:NSString.class] || handle.length < 8 || handle.length > 128) return NO;
  return [handle rangeOfCharacterFromSet:[[NSCharacterSet characterSetWithCharactersInString:@"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"] invertedSet]].location == NSNotFound;
}
static BOOL PorticoPublicationStateName(NSString *name) {
  if (![name isKindOfClass:NSString.class]) return NO;
  NSRange match = [name rangeOfString:@"^reader-state-[A-Za-z0-9_-]{1,64}\\.json$" options:NSRegularExpressionSearch];
  return match.location == 0 && match.length == name.length;
}
- (void)publicationStateInvalidate:(PorticoPublicationStateRegistry *)registry {
  dispatch_async(_queue, ^{registry.closed = YES; [registry.tickets removeAllObjects]; [registry.used removeAllObjects];});
}
- (void)publicationStateClose:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle {
  dispatch_async(_queue, ^{
    if (registry.closed || !PorticoPublicationStateHandle(handle)) return;
    if (registry.used.count >= 4096 && ![registry.used containsObject:handle]) {registry.closed = YES; [registry.tickets removeAllObjects]; [registry.used removeAllObjects]; return;}
    [registry.used addObject:handle]; [registry.tickets removeObjectForKey:handle];
  });
}
- (void)publicationStateOpen:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle identifier:(NSString *)identifier generation:(NSString *)expectedGeneration expected:(NSString *)expected resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    if (registry.closed || !PorticoPublicationStateHandle(handle) || [registry.used containsObject:handle]) {reject(@"reading_retired",@"reading_retired",nil);return;}
    if (registry.used.count >= 4096) {registry.closed = YES; [registry.tickets removeAllObjects]; [registry.used removeAllObjects]; reject(@"reading_retired",@"reading_retired",nil);return;}
    [registry.used addObject:handle]; // Failed opens cannot adopt a successor under this handle.
    NSData *raw = [expected isKindOfClass:NSString.class] ? [expected dataUsingEncoding:NSUTF8StringEncoding] : nil;
    if (!PorticoSafeIdentifier(identifier) || registry.tickets.count >= 8 || !raw || raw.length > 65536) {reject(@"reading_retired",@"reading_retired",nil);return;}
    PorticoPublicationGeneration *generation = self->_publicationGenerations[identifier];
    NSMutableDictionary *entry = self->_entries[identifier];
    NSArray *fields = PorticoPublicationStateFields(entry);
    id captured = [NSJSONSerialization JSONObjectWithData:raw options:0 error:nil];
    if (!generation || generation.retired || generation.entry != entry || ![generation.value isEqual:expectedGeneration] || ![entry[@"state"] isEqual:@"ready"] || !fields || ![fields isEqual:captured]) {reject(@"reading_retired",@"reading_retired",nil);return;}
    PorticoPublicationStateTicket *ticket = [PorticoPublicationStateTicket new]; ticket.identifier = identifier; ticket.generation = generation; ticket.fields = fields;
    registry.tickets[handle] = ticket; resolve(nil);
  });
}
- (PorticoPublicationStateTicket *)publicationStateTicket:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle name:(NSString *)name {
  if (registry.closed || !PorticoPublicationStateHandle(handle) || !PorticoPublicationStateName(name)) return nil;
  PorticoPublicationStateTicket *ticket = registry.tickets[handle]; if (!ticket) return nil;
  NSDictionary *entry = _entries[ticket.identifier];
  if (ticket.generation.retired || _publicationGenerations[ticket.identifier] != ticket.generation || entry != ticket.generation.entry || ![entry[@"state"] isEqual:@"ready"] || ![ticket.fields isEqual:PorticoPublicationStateFields(entry)]) return nil;
  return ticket;
}
- (void)publicationStateRead:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle name:(NSString *)name resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    PorticoPublicationStateTicket *ticket = [self publicationStateTicket:registry handle:handle name:name];
    if (!ticket) {reject(@"reading_retired",@"reading_retired",nil);return;}
    NSURL *file = [[self extrasFor:ticket.identifier] URLByAppendingPathComponent:name];
    NSError *error = nil;
    NSDictionary *attributes = [[NSFileManager defaultManager] attributesOfItemAtPath:file.path error:&error];
    if (!attributes && [error.domain isEqual:NSCocoaErrorDomain] && (error.code == NSFileNoSuchFileError || error.code == NSFileReadNoSuchFileError)) {resolve(@"");return;}
    if (![attributes[NSFileType] isEqual:NSFileTypeRegular]) {reject(@"reading_state_unavailable",@"reading_state_unavailable",nil);return;}
    NSNumber *size = attributes[NSFileSize];
    NSData *raw = size && size.longLongValue <= 1048576 ? [NSData dataWithContentsOfURL:file] : nil;
    NSString *value = raw ? [[NSString alloc] initWithData:raw encoding:NSUTF8StringEncoding] : nil;
    if (!value) {reject(@"reading_state_unavailable",@"reading_state_unavailable",nil);return;} resolve(value);
  });
}
- (void)publicationStateWrite:(PorticoPublicationStateRegistry *)registry handle:(NSString *)handle name:(NSString *)name json:(NSString *)json resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    PorticoPublicationStateTicket *ticket = [self publicationStateTicket:registry handle:handle name:name];
    NSData *raw = [json isKindOfClass:NSString.class] ? [json dataUsingEncoding:NSUTF8StringEncoding] : nil;
    if (!ticket || !raw || raw.length > 1048576 || ![NSJSONSerialization JSONObjectWithData:raw options:NSJSONReadingFragmentsAllowed error:nil]) {reject(@"reading_state_write_failed",@"reading_state_write_failed",nil);return;}
    NSURL *file = [[self extrasFor:ticket.identifier] URLByAppendingPathComponent:name];
    if (![[NSFileManager defaultManager] createDirectoryAtURL:[self extrasFor:ticket.identifier] withIntermediateDirectories:YES attributes:nil error:nil]) {reject(@"reading_state_write_failed",@"reading_state_write_failed",nil);return;}
    // No IO hop: proof and synchronous atomic replacement share close/remove/meta's owner turn.
    if ([self publicationStateTicket:registry handle:handle name:name] != ticket || ![raw writeToURL:file options:NSDataWritingAtomic | NSDataWritingFileProtectionCompleteUntilFirstUserAuthentication error:nil]) {reject(@"reading_state_write_failed",@"reading_state_write_failed",nil);return;}
    resolve(nil);
  });
}

// One small text file kept beside the download (its description, chapters and markers).
- (void)writeExtra:(NSString *)identifier name:(NSString *)name text:(NSString *)text resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    NSURL *file = [self extraURL:identifier name:name];
    NSData *data = [text isKindOfClass:NSString.class] ? [text dataUsingEncoding:NSUTF8StringEncoding] : nil;
    if (!file || !data || data.length > PorticoMaxExtraBytes) { reject(@"write_failed", @"This download's details could not be saved.", nil); return; }
    if ([name isEqual:@"publication.json"]) [self retirePublicationGeneration:identifier];
    [[NSFileManager defaultManager] createDirectoryAtURL:[self extrasFor:identifier] withIntermediateDirectories:YES attributes:nil error:nil];
    if (![data writeToURL:file options:NSDataWritingAtomic | NSDataWritingFileProtectionCompleteUntilFirstUserAuthentication error:nil]) { reject(@"write_failed", @"This download's details could not be saved.", nil); return; }
    resolve(nil);
  });
}
- (void)readExtra:(NSString *)identifier name:(NSString *)name resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    NSURL *file = [self extraURL:identifier name:name];
    NSNumber *size = file ? [[NSFileManager defaultManager] attributesOfItemAtPath:file.path error:nil][NSFileSize] : nil;
    NSData *raw = size && size.longLongValue <= PorticoMaxExtraBytes ? [NSData dataWithContentsOfURL:file] : nil;
    resolve(raw ? ([[NSString alloc] initWithData:raw encoding:NSUTF8StringEncoding] ?: @"") : @"");
  });
}
// Fetches one small file (a poster, a subtitle track) to keep beside the download. Answers
// "saved", "missing" (the server has no such file for this title, which is an ordinary answer),
// "later" (not ready yet, or the connection failed) or "invalid". These are small and fetched
// while the app is in front, so they use an ordinary session, under the same Wi-Fi rule.
- (void)fetchExtra:(NSString *)identifier name:(NSString *)name url:(NSString *)address resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    NSURL *file = [self extraURL:identifier name:name];
    NSMutableDictionary *incarnation = self->_entries[identifier];
    NSURL *url = [address isKindOfClass:NSString.class] ? [NSURL URLWithString:address] : nil;
    if (!file || !url || !([url.scheme isEqual:@"https"] || [url.scheme isEqual:@"http"])) { resolve(@"invalid"); return; }
    NSURLSessionDownloadTask *task = [PorticoServerTrust.session downloadTaskWithRequest:[self requestFor:url] completionHandler:^(NSURL *location, NSURLResponse *response, NSError *error) {
      NSInteger status = [response isKindOfClass:NSHTTPURLResponse.class] ? ((NSHTTPURLResponse *)response).statusCode : 0;
      NSNumber *size = location ? [[NSFileManager defaultManager] attributesOfItemAtPath:location.path error:nil][NSFileSize] : nil;
      NSString *outcome = @"later";
      NSURL *holding = nil;
      if (status == 404) outcome = @"missing";
      else if (!error && status >= 200 && status <= 299 && size.longLongValue > PorticoMaxExtraBytes) outcome = @"missing";
      else if (!error && status >= 200 && status <= 299 && size.longLongValue > 0) {
        // The temporary file is only valid during this call.
        holding = [[self root] URLByAppendingPathComponent:[NSString stringWithFormat:@"%@.extra", NSUUID.UUID.UUIDString]];
        if (![[NSFileManager defaultManager] moveItemAtURL:location toURL:holding error:nil]) holding = nil;
      }
      dispatch_async(self->_queue, ^{
        // The download may have been removed while this was in flight: leave nothing behind.
        if (!holding || self->_entries[identifier] != incarnation) { if (holding) [[NSFileManager defaultManager] removeItemAtURL:holding error:nil]; resolve(outcome); return; }
        [[NSFileManager defaultManager] createDirectoryAtURL:[self extrasFor:identifier] withIntermediateDirectories:YES attributes:nil error:nil];
        if ([name isEqual:@"publication.json"]) [self retirePublicationGeneration:identifier];
        [[NSFileManager defaultManager] removeItemAtURL:file error:nil];
        BOOL kept = [[NSFileManager defaultManager] moveItemAtURL:holding toURL:file error:nil];
        if (!kept) [[NSFileManager defaultManager] removeItemAtURL:holding error:nil];
        resolve(kept ? @"saved" : @"later");
      });
    }];
    [task resume];
  });
}

- (void)storage:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    NSDictionary *values = [[self root] resourceValuesForKeys:@[NSURLVolumeAvailableCapacityForImportantUsageKey, NSURLVolumeTotalCapacityKey] error:nil];
    long long used = 0;
    for (NSDictionary *entry in self->_entries.allValues) if ([entry[@"state"] isEqual:@"ready"]) used += [entry[@"bytes"] longLongValue];
    NSMutableDictionary *storage = [@{@"freeBytes": values[NSURLVolumeAvailableCapacityForImportantUsageKey] ?: @0, @"usedBytes": @(used)} mutableCopy];
    NSNumber *total = values[NSURLVolumeTotalCapacityKey];
    if ([total isKindOfClass:NSNumber.class] && total.longLongValue > 0 && total.unsignedLongLongValue <= PorticoStorageMaxSafe) storage[@"totalBytes"] = total;
    [storage addEntriesFromDictionary:PorticoDownloadStorage([self root])];
    resolve(storage);
  });
}

// On the owner queue, including paused policy waits that no longer have a session task.
- (void)restartCellularWait:(NSString *)identifier entry:(NSMutableDictionary *)entry {
  if (!self->_cellular || self->_entries[identifier] != entry || [entry[@"intent"] isEqual:@"hold"]
      || ![entry[@"waiting"] isEqual:@"wifi"] || [entry[@"state"] isEqual:@"ready"] || [entry[@"state"] isEqual:@"removed"]) return;
  NSURL *url = [entry[@"grantUrl"] isKindOfClass:NSString.class] ? [NSURL URLWithString:entry[@"grantUrl"]] : nil;
  if (!url || !([url.scheme isEqual:@"https"] || [url.scheme isEqual:@"http"])) return;
  [self beginTransfer:[entry copy] identifier:identifier url:url resolve:^(id row) {}];
}

- (void)setCellular:(BOOL)allowed resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  [[NSUserDefaults standardUserDefaults] setBool:allowed forKey:@"PorticoDownloadsCellular"];
  BOOL changed = self->_cellular != allowed;
  _cellular = allowed;
  if (!changed) { resolve(nil); return; }
  [[self session] getTasksWithCompletionHandler:^(NSArray *data, NSArray *uploads, NSArray<NSURLSessionDownloadTask *> *downloads) {
    dispatch_async(self->_queue, ^{ [self whenLoaded:^{
      for (NSString *identifier in self->_entries.allKeys) {
        NSMutableDictionary *incarnation = self->_entries[identifier];
        if ([incarnation[@"state"] isEqual:@"ready"] || [incarnation[@"state"] isEqual:@"removed"]) continue;
        // Tightening stops existing work promptly. Loosening only restarts a Wi-Fi wait
        // still meant to run, never a person's hold or an unrelated network failure.
        if (self->_cellular && (![incarnation[@"waiting"] isEqual:@"wifi"] || [incarnation[@"intent"] isEqual:@"hold"])) continue;
        NSURLSessionDownloadTask *task = self->_activeTasks[identifier];
        if (task && !PorticoDownloadAcceptsTask(incarnation, task.taskIdentifier)) task = nil;
        if (!task) for (NSURLSessionDownloadTask *candidate in downloads) {
          if ([candidate.taskDescription isEqual:identifier] && PorticoDownloadAcceptsTask(incarnation, candidate.taskIdentifier)) { task = candidate; break; }
        }
        if (!task) { [self restartCellularWait:identifier entry:incarnation]; continue; }
        BOOL resumed = [incarnation[@"resumed"] boolValue];
        [task cancelByProducingResumeData:^(NSData *bytes) {
          dispatch_async(self->_queue, ^{ [self whenLoaded:^{
            NSMutableDictionary *entry = self->_entries[identifier];
            if (entry != incarnation || !PorticoDownloadAcceptsTask(entry, task.taskIdentifier)) return;
            NSURL *resumeURL = [self resumeURLFor:identifier];
            if (bytes.length) [bytes writeToURL:resumeURL options:NSDataWritingAtomic error:nil];
            [self->_activeTasks removeObjectForKey:identifier];
            [entry removeObjectForKey:@"taskIdentifier"];
            [self retirePublicationGeneration:identifier];
            entry[@"state"] = @"paused"; entry[@"waiting"] = @"wifi";
            [self saveManifest]; [self changed:identifier force:YES];
            if (!self->_cellular || [entry[@"intent"] isEqual:@"hold"]) return;
            // Only a task with no saved prefix can safely adopt the new request's policy.
            // Otherwise beginTransfer reuses the blob unchanged, retaining its Wi-Fi rule.
            if (!resumed && task.countOfBytesReceived == 0
                && [entry[@"bytesDone"] isKindOfClass:NSNumber.class] && [entry[@"bytesDone"] longLongValue] == 0) {
              [[NSFileManager defaultManager] removeItemAtURL:resumeURL error:nil];
            }
            [self restartCellularWait:identifier entry:entry];
          }]; });
        }];
      }
    }]; });
  }];
  resolve(nil);
}

// One small JSON document for the JavaScript side's own bookkeeping (settings and the queue of
// viewing progress made while offline). Kept beside the files so removing the app's data
// removes both.
- (void)readState:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    self->_stateReadable = NO;
    NSError *error = nil;
    NSData *raw = [NSData dataWithContentsOfURL:[self stateURL] options:0 error:&error];
    if (!raw && PorticoDownloadMissingFile(error)) { self->_stateReadable = YES; resolve(@""); return; }
    NSString *text = raw ? [[NSString alloc] initWithData:raw encoding:NSUTF8StringEncoding] : nil;
    NSDictionary *stored = text ? [NSJSONSerialization JSONObjectWithData:raw options:0 error:&error] : nil;
    // JS validates the version and individual outbox/settings fields before its write barrier
    // opens. Native must never turn unreadable bytes or a non-document into the empty sentinel.
    if (!text || ![stored isKindOfClass:NSDictionary.class]) {
      reject(@"read_failed", @"Download settings could not be read. Keep this device's data and try again after storage is available.", error); return;
    }
    self->_stateReadable = YES;
    resolve(text);
  });
}
- (void)writeState:(NSString *)json resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    if (!self->_stateReadable) { reject(@"read_failed", @"Read download settings successfully before saving changes.", nil); return; }
    if (json.length >= 1048576) { reject(@"too_large", @"Download settings are too large to keep.", nil); return; }
    NSError *error = nil;
    if (![[json dataUsingEncoding:NSUTF8StringEncoding] writeToURL:[self stateURL] options:NSDataWritingAtomic | NSDataWritingFileProtectionCompleteUntilFirstUserAuthentication error:&error]) {
      reject(@"write_failed", @"Download settings could not be saved.", error); return;
    }
    resolve(nil);
  });
}

#pragma mark - Offline playback

// OS-07: the file of a finished download, for Portico's own player (PorticoPlaybackController
// accepts a file source only inside this module's root).
- (void)publicationOpen:(NSString *)handle identifier:(NSString *)identifier owner:(NSString *)owner digest:(NSString *)digest reads:(PorticoPublicationReads *)reads resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
 dispatch_async(_queue,^{
  NSDictionary *entry=self->_entries[identifier];
  BOOL(^current)(void)=^BOOL{
   NSString *raw=entry[@"meta"];NSDictionary *meta=[raw isKindOfClass:NSString.class]?[NSJSONSerialization JSONObjectWithData:[raw dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil]:nil;
   return self->_entries[identifier]==entry&&[entry[@"state"] isEqual:@"ready"]&&[entry[@"sha256"] isEqual:digest]&&[meta isKindOfClass:NSDictionary.class]&&[meta[@"owner"] isEqual:owner];
  };
  if(!current()){reject(@"reading_unavailable",@"reading_unavailable",nil);return;}
  NSString *name=entry[@"file"];if(![name isKindOfClass:NSString.class]){reject(@"reading_unavailable",@"reading_unavailable",nil);return;}
  NSURL *file=[[self directoryFor:identifier] URLByAppendingPathComponent:PorticoSafeFileName(name)];
  [reads open:handle file:file bytes:[entry[@"bytes"] longLongValue] digest:digest queue:self->_queue current:current resolve:resolve reject:reject];
 });
}

- (void)localFile:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  dispatch_async(_queue, ^{
    NSDictionary *entry = self->_entries[identifier];
    NSString *file = entry[@"file"];
    if (![entry[@"state"] isEqual:@"ready"] || ![file isKindOfClass:NSString.class]) { reject(@"not_ready", @"This download is not ready to play.", nil); return; }
    NSURL *url = [[self directoryFor:identifier] URLByAppendingPathComponent:PorticoSafeFileName(file)];
    if (![[NSFileManager defaultManager] fileExistsAtPath:url.path]) { reject(@"missing_file", @"This download’s file is missing. Remove it and download it again.", nil); return; }
    // The player's timeline needs the length before the first frame, so read it from the file.
    AVURLAsset *asset = [AVURLAsset URLAssetWithURL:url options:nil];
    [asset loadValuesAsynchronouslyForKeys:@[@"duration"] completionHandler:^{
      double seconds = [asset statusOfValueForKey:@"duration" error:nil] == AVKeyValueStatusLoaded ? CMTimeGetSeconds(asset.duration) : 0;
      resolve(@{@"url": url.absoluteString, @"durationSeconds": @(isfinite(seconds) && seconds > 0 ? seconds : 0)});
    }];
  });
}

@end

#pragma mark - React Native

// The JavaScript side of the owner above. It holds nothing of its own: every call runs on the
// owner's queue, those about downloads once the manifest has been read (or are refused if it
// cannot be read although the device is unlocked), and the owner's changes reach JavaScript
// through it while JavaScript is listening. The settings document, the cellular preference
// (whose per-transfer bookkeeping waits for the manifest inside the owner) and nothing else are
// independent of the manifest. Free space is too, but the space downloads use comes from it.
static void PorticoRefuseUnreadable(RCTPromiseRejectBlock reject) {
  reject(@"manifest_unreadable", @"Downloads on this device can't be read. Keep this device's data and try again after storage is available.", nil);
}

// Cover-page uploads: the app sends only bounded image bytes; this module
// generates the private cache file, its opaque handle and its file URI.
static NSString *PorticoCoverActualMime(NSData *data) {
  const unsigned char *bytes = data.bytes;
  NSUInteger length = data.length;
  if (length >= 3 && bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF) return @"image/jpeg";
  if (length >= 8 && bytes[0] == 0x89 && bytes[1] == 0x50 && bytes[2] == 0x4E && bytes[3] == 0x47
      && bytes[4] == 0x0D && bytes[5] == 0x0A && bytes[6] == 0x1A && bytes[7] == 0x0A) return @"image/png";
  if (length >= 12 && bytes[0] == 0x52 && bytes[1] == 0x49 && bytes[2] == 0x46 && bytes[3] == 0x46
      && bytes[8] == 0x57 && bytes[9] == 0x45 && bytes[10] == 0x42 && bytes[11] == 0x50) return @"image/webp";
  return nil;
}
static NSString *PorticoCoverExtension(NSString *mime) {
  if ([mime isEqualToString:@"image/jpeg"]) return @".jpg";
  if ([mime isEqualToString:@"image/png"]) return @".png";
  return @".webp";
}
/** Strict canonical base64 for one chunk (at most 64 KiB of bytes; the app
 * sends 24 KiB): bounded before decode, multiple of four, no whitespace,
 * ignored character or malformed tail. */
static BOOL PorticoCoverBase64Valid(NSString *value) {
  const NSUInteger maximum = 87384;
  NSUInteger length = value.length;
  if (length == 0 || length > maximum || length % 4 != 0) return NO;
  const char *bytes = value.UTF8String;
  if (!bytes) return NO;
  NSUInteger padding = 0;
  for (NSUInteger index = 0; index < length; index++) {
    char c = bytes[index];
    if (c == '=') {padding++; if (padding > 2 || index < length - 2) return NO; continue;}
    if (padding) return NO;
    if (!((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/')) return NO;
  }
  return YES;
}
static NSString *PorticoCoverDirectory(NSError **error) {
  NSString *root = NSSearchPathForDirectoriesInDomains(NSCachesDirectory, NSUserDomainMask, YES).firstObject;
  if (!root) { if (error) *error = [NSError errorWithDomain:@"portico_cover" code:1 userInfo:nil]; return nil; }
  NSString *directory = [root stringByAppendingPathComponent:@"portico-cover-uploads"];
  if (![[NSFileManager defaultManager] createDirectoryAtPath:directory withIntermediateDirectories:YES attributes:nil error:error]) return nil;
  return directory;
}

/** A cover-page temp file this module created, filled in bounded chunks and then finished. */
@interface PorticoCoverStage : NSObject
@property (nonatomic, copy) NSString *path;
@property (nonatomic, copy) NSString *mime;
@property (nonatomic) NSUInteger generation;
@property (nonatomic) unsigned long long size;
@property (nonatomic) BOOL finished;
@end
@implementation PorticoCoverStage
@end

@implementation PorticoDownloads {
  PorticoDownloadOwner *_owner;
  PorticoPublicationReads *_publicationReads;
  PorticoPublicationStateRegistry *_publicationState;
  NSMutableDictionary<NSString *, PorticoCoverStage *> *_coverFiles;
  NSLock *_coverLock;
  NSUInteger _coverGeneration;
  BOOL _coverRetired;
}

RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return NO; }
- (instancetype)init {
  if ((self = [super init])) {
    _owner = [PorticoDownloadOwner shared];
    _publicationReads=[PorticoPublicationReads new];
    _publicationState=[PorticoPublicationStateRegistry new];
    _coverFiles=[NSMutableDictionary dictionary];
    _coverLock=[NSLock new];
    _owner.emitter = self;
    // Another wake: the owner reads whether the device is unlocked on the main queue.
    dispatch_async(dispatch_get_main_queue(), ^{ [PorticoDownloadOwner start]; });
  }
  return self;
}
- (void)invalidate {[_owner publicationStateInvalidate:_publicationState];[_publicationReads invalidate];[_coverLock lock];_coverRetired=YES;_coverGeneration++;NSArray<NSString *> *paths=[_coverFiles.allValues valueForKey:@"path"];[_coverFiles removeAllObjects];[_coverLock unlock];for (NSString *path in paths) [[NSFileManager defaultManager] removeItemAtPath:path error:nil];[super invalidate];}
- (dispatch_queue_t)methodQueue { return _owner.queue; }
- (NSArray<NSString *> *)supportedEvents { return @[PorticoDownloadChanged]; }
- (void)startObserving { _porticoObserving = YES; }
- (void)stopObserving { _porticoObserving = NO; }

RCT_EXPORT_METHOD(list:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner list:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(start:(NSDictionary *)request resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner start:request resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(resume:(NSDictionary *)request resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner resume:request resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(pause:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner pause:identifier resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(remove:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner remove:identifier resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(removeGeneration:(NSString *)identifier generation:(NSString *)generation resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner remove:identifier generation:generation resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(setMeta:(NSString *)identifier meta:(NSString *)meta resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner setMeta:identifier meta:meta resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(setMetas:(NSDictionary *)metas resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner setMetas:metas resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(publicationStateOpen:(NSString *)handle identifier:(NSString *)identifier generation:(NSString *)generation expected:(NSString *)expected resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{[self->_owner publicationStateOpen:self->_publicationState handle:handle identifier:identifier generation:generation expected:expected resolve:resolve reject:reject];} otherwise:^{PorticoRefuseUnreadable(reject);}];
}
RCT_EXPORT_METHOD(publicationStateRead:(NSString *)handle name:(NSString *)name resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{[self->_owner publicationStateRead:self->_publicationState handle:handle name:name resolve:resolve reject:reject];} otherwise:^{PorticoRefuseUnreadable(reject);}];
}
RCT_EXPORT_METHOD(publicationStateWrite:(NSString *)handle name:(NSString *)name json:(NSString *)json resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{[self->_owner publicationStateWrite:self->_publicationState handle:handle name:name json:json resolve:resolve reject:reject];} otherwise:^{PorticoRefuseUnreadable(reject);}];
}
RCT_EXPORT_METHOD(publicationStateClose:(NSString *)handle) {[_owner publicationStateClose:_publicationState handle:handle];}
RCT_EXPORT_METHOD(writeExtra:(NSString *)identifier name:(NSString *)name text:(NSString *)text resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner writeExtra:identifier name:name text:text resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(readExtra:(NSString *)identifier name:(NSString *)name resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner readExtra:identifier name:name resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(fetchExtra:(NSString *)identifier name:(NSString *)name url:(NSString *)address resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner fetchExtra:identifier name:name url:address resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(storage:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner storage:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}
RCT_EXPORT_METHOD(setCellular:(BOOL)allowed resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner setCellular:allowed resolve:resolve reject:reject];
}
RCT_EXPORT_METHOD(readState:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner readState:resolve reject:reject];
}
RCT_EXPORT_METHOD(writeState:(NSString *)json resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner writeState:json resolve:resolve reject:reject];
}
RCT_EXPORT_METHOD(localFile:(NSString *)identifier resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  [_owner whenLoaded:^{ [self->_owner localFile:identifier resolve:resolve reject:reject]; } otherwise:^{ PorticoRefuseUnreadable(reject); }];
}

RCT_EXPORT_METHOD(publicationOpen:(NSString *)handle identifier:(NSString *)identifier owner:(NSString *)owner digest:(NSString *)digest resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
 [_owner whenLoaded:^{[self->_owner publicationOpen:handle identifier:identifier owner:owner digest:digest reads:self->_publicationReads resolve:resolve reject:reject];} otherwise:^{PorticoRefuseUnreadable(reject);}];
}
RCT_EXPORT_METHOD(publicationRead:(NSString *)handle operation:(double)operation offset:(double)offset length:(double)length resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {[_publicationReads range:handle operation:operation offset:offset length:length member:NO resolve:resolve reject:reject];}
RCT_EXPORT_METHOD(publicationMember:(NSString *)handle operation:(double)operation offset:(double)offset compressed:(double)compressed size:(double)size method:(double)method digest:(NSString *)digest resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {[_publicationReads member:handle operation:operation offset:offset compressed:compressed size:size method:method digest:digest resolve:resolve reject:reject];}
RCT_EXPORT_METHOD(publicationMemberRead:(NSString *)handle operation:(double)operation offset:(double)offset length:(double)length resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {[_publicationReads range:handle operation:operation offset:offset length:length member:YES resolve:resolve reject:reject];}
RCT_EXPORT_METHOD(publicationCancel:(NSString *)handle operation:(double)operation) {[_publicationReads cancel:handle operation:operation];}
RCT_EXPORT_METHOD(publicationClose:(NSString *)handle) {[_publicationReads close:handle];}

// One bounded image-only private temp file, staged in chunks so the app never
// holds the whole image as one string (IM01). The caller sends bytes only; the
// filename, directory and opaque handle are generated here. A stage that loses
// its module generation never finishes and is deleted. Calls run in order on
// the module's queue.
RCT_EXPORT_METHOD(beginCoverImage:(NSString *)mime resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  if (![mime isKindOfClass:NSString.class] || (![mime isEqualToString:@"image/jpeg"] && ![mime isEqualToString:@"image/png"] && ![mime isEqualToString:@"image/webp"])) {reject(@"unsupported_image", @"unsupported_image", nil);return;}
  NSError *error = nil;
  NSString *directory = PorticoCoverDirectory(&error);
  if (!directory) {reject(@"write_failed", @"write_failed", error);return;}
  NSString *path = [directory stringByAppendingPathComponent:[[NSUUID UUID].UUIDString stringByAppendingString:PorticoCoverExtension(mime)]];
  if (![[NSFileManager defaultManager] createFileAtPath:path contents:[NSData data] attributes:nil]) {reject(@"write_failed", @"write_failed", nil);return;}
  PorticoCoverStage *stage = [PorticoCoverStage new];
  stage.path = path; stage.mime = mime;
  NSString *handle = [NSUUID UUID].UUIDString;
  BOOL registered = NO;
  [_coverLock lock];
  if (!_coverRetired) {stage.generation=_coverGeneration;_coverFiles[handle]=stage;registered=YES;}
  [_coverLock unlock];
  if (!registered) {[[NSFileManager defaultManager] removeItemAtPath:path error:nil];reject(@"cancelled", @"cancelled", nil);return;}
  resolve(@{@"handle":handle});
}
/** The stage when it is known, unfinished and of the current generation. */
- (PorticoCoverStage *)openCoverStage:(NSString *)handle {
  if (![handle isKindOfClass:NSString.class]) return nil;
  [_coverLock lock];
  PorticoCoverStage *stage = _coverFiles[handle];
  if (stage.finished || _coverRetired || stage.generation != _coverGeneration) stage = nil;
  [_coverLock unlock];
  return stage;
}
// Appends one chunk; answers the bytes staged so far.
RCT_EXPORT_METHOD(appendCoverImage:(NSString *)handle base64:(NSString *)base64 resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  if (![base64 isKindOfClass:NSString.class] || !PorticoCoverBase64Valid(base64)) {reject(@"invalid_image", @"invalid_image", nil);return;}
  NSData *data = [[NSData alloc] initWithBase64EncodedString:base64 options:0];
  // Canonical equality also rejects non-zero unused tail bits in the last sextet.
  if (data.length == 0 || data.length > 64 * 1024 || ![[data base64EncodedStringWithOptions:0] isEqualToString:base64]) {reject(@"invalid_image", @"invalid_image", nil);return;}
  PorticoCoverStage *stage = [self openCoverStage:handle];
  if (!stage) {reject(@"invalid_handle", @"invalid_handle", nil);return;}
  if (stage.size + data.length > 10 * 1024 * 1024) {reject(@"invalid_image", @"invalid_image", nil);return;}
  NSError *error = nil;
  NSFileHandle *file = [NSFileHandle fileHandleForWritingToURL:[NSURL fileURLWithPath:stage.path] error:&error];
  if (!file || ![file seekToEndReturningOffset:nil error:&error] || ![file writeData:data error:&error]) {[file closeAndReturnError:nil];reject(@"write_failed", @"write_failed", error);return;}
  if (![file closeAndReturnError:&error]) {reject(@"write_failed", @"write_failed", error);return;}
  stage.size += data.length;
  resolve(@(stage.size));
}
// The staged file is exactly `size` bytes of the declared image type: it becomes the lease.
RCT_EXPORT_METHOD(finishCoverImage:(NSString *)handle size:(double)size resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  PorticoCoverStage *stage = [self openCoverStage:handle];
  if (!stage) {reject(@"invalid_handle", @"invalid_handle", nil);return;}
  NSNumber *actual = [[NSFileManager defaultManager] attributesOfItemAtPath:stage.path error:nil][NSFileSize];
  if (size < 1 || size != floor(size) || (unsigned long long)size != stage.size || actual.unsignedLongLongValue != stage.size) {reject(@"invalid_image", @"invalid_image", nil);return;}
  NSError *error = nil;
  NSFileHandle *file = [NSFileHandle fileHandleForUpdatingURL:[NSURL fileURLWithPath:stage.path] error:&error];
  NSData *head = [file readDataUpToLength:12 error:&error];
  if (!file || !head) {[file closeAndReturnError:nil];reject(@"write_failed", @"write_failed", error);return;}
  if (![PorticoCoverActualMime(head) isEqualToString:stage.mime]) {[file closeAndReturnError:nil];reject(@"unsupported_image", @"unsupported_image", nil);return;}
  BOOL synced = [file synchronizeAndReturnError:&error];
  [file closeAndReturnError:nil];
  if (!synced) {reject(@"write_failed", @"write_failed", error);return;}
  BOOL finished = NO;
  [_coverLock lock];
  if (!_coverRetired && stage.generation == _coverGeneration) {stage.finished=YES;finished=YES;}
  [_coverLock unlock];
  if (!finished) {[[NSFileManager defaultManager] removeItemAtPath:stage.path error:nil];reject(@"cancelled", @"cancelled", nil);return;}
  resolve(@{@"handle":handle, @"uri":[NSURL fileURLWithPath:stage.path].absoluteString, @"size":@(stage.size)});
}
RCT_EXPORT_METHOD(deleteCoverImage:(NSString *)handle resolve:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject) {
  if (![handle isKindOfClass:NSString.class]) {resolve(nil);return;}
  [_coverLock lock];NSString *path = _coverFiles[handle].path;[_coverLock unlock];
  if (!path) {resolve(nil);return;}
  NSString *directory = PorticoCoverDirectory(nil);
  if (!directory || ![[path stringByDeletingLastPathComponent] isEqualToString:directory]) {
    [_coverLock lock];[_coverFiles removeObjectForKey:handle];[_coverLock unlock];
    reject(@"invalid_handle", @"invalid_handle", nil);return;
  }
  NSError *error = nil;
  if ([[NSFileManager defaultManager] removeItemAtPath:path error:&error] || ![[NSFileManager defaultManager] fileExistsAtPath:path]) {
    [_coverLock lock];[_coverFiles removeObjectForKey:handle];[_coverLock unlock];
    resolve(nil);
  } else reject(@"delete_failed", @"delete_failed", error);
}
@end
