#import <React/RCTEventEmitter.h>
#import <React/RCTBridgeModule.h>
#import <AVFoundation/AVFoundation.h>
#import <MediaPlayer/MediaPlayer.h>
#import <ImageIO/ImageIO.h>
#import <UIKit/UIKit.h>
#import "PorticoPlaybackController.h"
#import "PorticoServerTrust.h"

/**
 * Video's presence in the system: Now Playing on the lock screen, Control Centre, the Apple
 * Watch, AirPods and the Apple TV's Control Centre, plus the remote commands they send, audio
 * interruptions and the AirPlay route (OS-01, OS-03, OS-06).
 *
 * Music and audiobooks are not handled here: the native listening owner already publishes them
 * and owns their commands. This module stands aside whenever that owner is active.
 *
 * It never controls the player itself. Every command becomes an event the JavaScript player
 * turns into its usual intent (so Watch Together rules, revisions and progress reporting stay in
 * one place). It only reads the player's clock to keep the elapsed time honest.
 *
 * Artwork is fetched from the viewer's own server with the viewer's bearer, from the one
 * protected artwork path shape the app uses, without following redirects, and bounded to 4 MB.
 */
@interface PorticoSystemMedia : RCTEventEmitter <RCTBridgeModule, NSURLSessionTaskDelegate>
@end

static BOOL PorticoSMText(id value, NSUInteger limit) {
  if (![value isKindOfClass:NSString.class] || [value length] > limit) return NO;
  for (NSUInteger i = 0; i < [value length]; i++) { unichar c = [value characterAtIndex:i]; if (c < 32 || c == 127) return NO; }
  return YES;
}
static BOOL PorticoSMNumber(id value) { return [value isKindOfClass:NSNumber.class] && CFGetTypeID((__bridge CFTypeRef)value) != CFBooleanGetTypeID() && isfinite([value doubleValue]); }

@implementation PorticoSystemMedia {
  BOOL _listening, _active, _registered, _interrupted;
  NSDictionary *_info;          // validated video description
  NSArray<NSDictionary *> *_chapters, *_markers;
  MPMediaItemArtwork *_artwork; NSString *_artworkKey; NSURLSessionDataTask *_artworkTask; NSURLSession *_session;
  NSMutableArray<NSDictionary *> *_targets;
  AVPlayer *_player; NSTimer *_clock;
  NSString *_routeName; BOOL _external, _subtitlesCarried; NSString *_commandSignature;
}
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (NSArray<NSString *> *)supportedEvents { return @[@"PorticoSystemMediaCommand", @"PorticoSystemMediaRoute"]; }
- (void)startObserving { _listening = YES; }
- (void)stopObserving { _listening = NO; }

- (instancetype)init {
  if ((self = [super init])) {
    _targets = [NSMutableArray new]; _routeName = @""; _subtitlesCarried = YES;
    NSNotificationCenter *center = NSNotificationCenter.defaultCenter;
    [center addObserver:self selector:@selector(interruption:) name:AVAudioSessionInterruptionNotification object:nil];
    [center addObserver:self selector:@selector(routeChanged:) name:AVAudioSessionRouteChangeNotification object:nil];
    [center addObserver:self selector:@selector(timeJumped:) name:AVPlayerItemTimeJumpedNotification object:nil];
  }
  return self;
}

- (PorticoPlaybackController *)controller {
  if (PorticoPlaybackController.shared) return PorticoPlaybackController.shared;
  id module = [self.bridge moduleForClass:NSClassFromString(@"PorticoPlaybackModule")];
  id controller = [module valueForKey:@"controller"];
  return [controller isKindOfClass:PorticoPlaybackController.class] ? controller : nil;
}
- (void)send:(NSString *)name body:(NSDictionary *)body { if (_listening) [self sendEventWithName:name body:body]; }

#pragma mark - JavaScript surface

/** Describes the video now on screen. Called again whenever any field changes; cheap. */
RCT_EXPORT_METHOD(setVideo:(NSDictionary *)raw) {
  NSDictionary *info = [self validated:raw];
  if (!info) { [self clearVideo]; return; }
  PorticoPlaybackController *controller = [self controller];
  if (controller.listeningActive) { [self clearVideo]; return; } // music/audiobooks own the system surface
  BOOL itemChanged = ![_info[@"itemId"] isEqual:info[@"itemId"]];
  _info = info; _chapters = info[@"chapters"]; _markers = info[@"markers"]; _active = YES;
  if (itemChanged) { _artwork = nil; }
  [self bindPlayer];
  // Re-register only when something the commands depend on changed: re-registering disables
  // every command for a moment, which tells the system the app can't be Now Playing.
  NSString *signature = [NSString stringWithFormat:@"%@|%@|%@|%@|%@|%lu|%lu", info[@"live"], info[@"skipBackSeconds"], info[@"skipForwardSeconds"], info[@"canNext"], info[@"canPrevious"], (unsigned long)_chapters.count, (unsigned long)_markers.count];
  if (!_registered || ![signature isEqual:_commandSignature]) { [self registerCommands]; _commandSignature = signature; }
  [self loadArtwork];
  [self publish];
  if (!_clock) { __weak PorticoSystemMedia *weakSelf = self; _clock = [NSTimer scheduledTimerWithTimeInterval:2 repeats:YES block:^(NSTimer *timer) { [weakSelf tick]; }]; }
}

/** The video left the screen (closed, finished, or became music). */
RCT_EXPORT_METHOD(clear) { [self clearVideo]; }

RCT_REMAP_METHOD(currentRoute, currentRouteWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  [self bindPlayer]; resolve([self routeBody]);
}

#pragma mark - Validation

- (NSDictionary *)validated:(NSDictionary *)raw {
  if (![raw isKindOfClass:NSDictionary.class]) return nil;
  NSString *itemId = raw[@"itemId"], *title = raw[@"title"];
  if (!PorticoSMText(itemId, 128) || ![itemId length] || !PorticoSMText(title, 512) || ![title length]) return nil;
  NSMutableDictionary *out = [@{@"itemId": itemId, @"title": title} mutableCopy];
  NSDictionary<NSString *, NSNumber *> *limits = @{@"subtitle": @512, @"origin": @1024, @"bearer": @8192, @"posterPath": @1024, @"sourceToken": @128};
  for (NSString *key in limits) if (PorticoSMText(raw[key], limits[key].unsignedIntegerValue) && [raw[key] length]) out[key] = raw[key];
  out[@"durationSeconds"] = PorticoSMNumber(raw[@"durationSeconds"]) && [raw[@"durationSeconds"] doubleValue] > 0 ? raw[@"durationSeconds"] : @0;
  out[@"live"] = @([raw[@"live"] boolValue]);
  out[@"canNext"] = @([raw[@"canNext"] boolValue]); out[@"canPrevious"] = @([raw[@"canPrevious"] boolValue]);
  double back = PorticoSMNumber(raw[@"skipBackSeconds"]) ? [raw[@"skipBackSeconds"] doubleValue] : 10, forward = PorticoSMNumber(raw[@"skipForwardSeconds"]) ? [raw[@"skipForwardSeconds"] doubleValue] : 30;
  out[@"skipBackSeconds"] = @(MAX(5, MIN(120, back))); out[@"skipForwardSeconds"] = @(MAX(5, MIN(120, forward)));
  NSMutableArray *chapters = [NSMutableArray new];
  if ([raw[@"chapters"] isKindOfClass:NSArray.class]) for (NSDictionary *c in [raw[@"chapters"] subarrayWithRange:NSMakeRange(0, MIN((NSUInteger)500, [raw[@"chapters"] count]))]) {
    if ([c isKindOfClass:NSDictionary.class] && PorticoSMNumber(c[@"startSeconds"]) && [c[@"startSeconds"] doubleValue] >= 0) [chapters addObject:@{@"startSeconds": c[@"startSeconds"], @"title": PorticoSMText(c[@"title"], 256) ? c[@"title"] : @""}];
  }
  [chapters sortUsingComparator:^NSComparisonResult(NSDictionary *a, NSDictionary *b) { return [a[@"startSeconds"] compare:b[@"startSeconds"]]; }];
  NSMutableArray *markers = [NSMutableArray new];
  if ([raw[@"markers"] isKindOfClass:NSArray.class]) for (NSDictionary *m in [raw[@"markers"] subarrayWithRange:NSMakeRange(0, MIN((NSUInteger)50, [raw[@"markers"] count]))]) {
    if ([m isKindOfClass:NSDictionary.class] && PorticoSMText(m[@"kind"], 32) && PorticoSMNumber(m[@"startSeconds"]) && PorticoSMNumber(m[@"endSeconds"]) && [m[@"endSeconds"] doubleValue] > [m[@"startSeconds"] doubleValue])
      [markers addObject:@{@"kind": m[@"kind"], @"startSeconds": m[@"startSeconds"], @"endSeconds": m[@"endSeconds"]}];
  }
  out[@"chapters"] = chapters; out[@"markers"] = markers;
  return out;
}

#pragma mark - Player clock and route

- (void)bindPlayer {
  AVPlayer *player = [self controller].player;
  if (player == _player) return;
  AVPlayer *old = _player;
  if (old) { @try { [old removeObserver:self forKeyPath:@"rate"]; [old removeObserver:self forKeyPath:@"externalPlaybackActive"]; } @catch (NSException *e) {} }
  _player = player;
  if (!player) return;
#if !TARGET_OS_TV
  player.allowsExternalPlayback = YES;
  player.usesExternalPlaybackWhileExternalScreenIsActive = YES;
#endif
  [player addObserver:self forKeyPath:@"rate" options:NSKeyValueObservingOptionNew context:NULL];
  [player addObserver:self forKeyPath:@"externalPlaybackActive" options:NSKeyValueObservingOptionNew context:NULL];
  [self updateRoute];
}
- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context {
  dispatch_async(dispatch_get_main_queue(), ^{
    if ([keyPath isEqual:@"externalPlaybackActive"]) [self updateRoute];
    else [self publish];
  });
}
- (void)timeJumped:(NSNotification *)note { if (_active && note.object == _player.currentItem) dispatch_async(dispatch_get_main_queue(), ^{ [self publish]; }); }
- (void)tick { if (!_active) { [_clock invalidate]; _clock = nil; return; } [self bindPlayer]; if ([self controller].listeningActive) { [self clearVideo]; return; } [self updateRoute]; [self publish]; }

- (double)position {
  PorticoPlaybackController *controller = [self controller];
  double p = controller ? [controller currentSourcePosition] : NAN;
  return isfinite(p) ? MAX(0, p) : 0;
}

- (NSDictionary *)routeBody {
  NSString *kind = @"none";
  AVAudioSessionPortDescription *port = AVAudioSession.sharedInstance.currentRoute.outputs.firstObject;
  if (_player.externalPlaybackActive || [port.portType isEqual:AVAudioSessionPortAirPlay]) kind = @"airplay";
  else if ([port.portType isEqual:AVAudioSessionPortHDMI]) kind = @"hdmi";
  NSString *name = [kind isEqual:@"none"] ? @"" : (port.portName ?: @"");
  BOOL external = _player.externalPlaybackActive;
  // OS-02: subtitles drawn by the app stay on the phone; the receiver only gets the stream.
  return @{@"external": @(external), @"kind": kind, @"name": name, @"subtitlesCarried": @(!(external && [self controller].subtitleOverlayActive))};
}
- (void)updateRoute {
  NSDictionary *body = [self routeBody];
  BOOL external = [body[@"external"] boolValue], carried = [body[@"subtitlesCarried"] boolValue]; NSString *name = body[@"name"];
  if (external == _external && [name isEqual:_routeName] && carried == _subtitlesCarried) return;
  _external = external; _routeName = name; _subtitlesCarried = carried;
  [self send:@"PorticoSystemMediaRoute" body:body];
}

#pragma mark - Now Playing

- (void)publish {
  if (!_active || !_info) return;
  if ([self controller].listeningActive) return;
  BOOL live = [_info[@"live"] boolValue];
  NSMutableDictionary *now = [NSMutableDictionary new];
  now[MPMediaItemPropertyTitle] = _info[@"title"];
  if (_info[@"subtitle"]) now[MPMediaItemPropertyArtist] = _info[@"subtitle"];
  now[MPNowPlayingInfoPropertyMediaType] = @(MPNowPlayingInfoMediaTypeVideo);
  now[MPNowPlayingInfoPropertyIsLiveStream] = @(live);
  now[MPNowPlayingInfoPropertyExternalContentIdentifier] = _info[@"itemId"];
  now[MPNowPlayingInfoPropertyDefaultPlaybackRate] = @1;
  float rate = _player ? _player.rate : 0;
  now[MPNowPlayingInfoPropertyPlaybackRate] = @(rate);
  if (!live) {
    double duration = [_info[@"durationSeconds"] doubleValue];
    if (duration <= 0 && _player.currentItem) { double d = CMTimeGetSeconds(_player.currentItem.duration); if (isfinite(d) && d > 0) duration = d; }
    if (duration > 0) now[MPMediaItemPropertyPlaybackDuration] = @(duration);
    double position = [self position];
    now[MPNowPlayingInfoPropertyElapsedPlaybackTime] = @(duration > 0 ? MIN(position, duration) : position);
    if (_chapters.count) {
      NSUInteger current = 0; for (NSUInteger i = 0; i < _chapters.count; i++) if ([_chapters[i][@"startSeconds"] doubleValue] <= position + 0.5) current = i;
      now[MPNowPlayingInfoPropertyChapterNumber] = @(current); now[MPNowPlayingInfoPropertyChapterCount] = @(_chapters.count);
    }
  }
  if (_artwork) now[MPMediaItemPropertyArtwork] = _artwork;
  MPNowPlayingInfoCenter *center = MPNowPlayingInfoCenter.defaultCenter;
  center.nowPlayingInfo = now;
}

- (void)clearVideo {
  BOOL was = _active;
  _active = NO; _info = nil; _chapters = nil; _markers = nil; _artwork = nil; _artworkKey = nil; [_artworkTask cancel]; _artworkTask = nil;
  [_clock invalidate]; _clock = nil;
  [self unregisterCommands];
  // Only clear what this module published: the listening owner may be showing music now.
  if (was && ![self controller].listeningActive) MPNowPlayingInfoCenter.defaultCenter.nowPlayingInfo = nil;
}

#pragma mark - Artwork

- (void)loadArtwork {
  NSString *origin = _info[@"origin"], *path = _info[@"posterPath"], *bearer = _info[@"bearer"];
  if (!origin || !path) return;
  NSString *key = [origin stringByAppendingString:path];
  if ([key isEqual:_artworkKey]) return;
  NSURL *base = [NSURL URLWithString:origin];
  NSRegularExpression *shape = [NSRegularExpression regularExpressionWithPattern:@"^/v1/items/[A-Za-z0-9_-]{1,128}/art/(poster|backdrop|thumb)$" options:0 error:nil];
  if (!base || !([base.scheme isEqual:@"https"] || [base.scheme isEqual:@"http"]) || base.user || base.password || base.query || base.fragment || (base.path.length && ![base.path isEqual:@"/"]) || ![shape firstMatchInString:path options:0 range:NSMakeRange(0, path.length)]) return;
  NSURL *url = [NSURL URLWithString:path relativeToURL:base].absoluteURL;
  if (!url) return;
  _artworkKey = key; [_artworkTask cancel];
  if (!_session) {
    NSURLSessionConfiguration *configuration = NSURLSessionConfiguration.ephemeralSessionConfiguration;
    configuration.URLCache = nil; configuration.HTTPCookieStorage = nil; configuration.timeoutIntervalForRequest = 15; configuration.timeoutIntervalForResource = 20;
    // Decoding happens on this background queue, never on the main thread (weak Apple TVs).
    NSOperationQueue *queue = [NSOperationQueue new]; queue.maxConcurrentOperationCount = 1;
    _session = [NSURLSession sessionWithConfiguration:configuration delegate:self delegateQueue:queue];
  }
  NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];
  if (bearer) [request setValue:[@"Bearer " stringByAppendingString:bearer] forHTTPHeaderField:@"Authorization"];
  [request setValue:@"image/*" forHTTPHeaderField:@"Accept"];
  __weak PorticoSystemMedia *weakSelf = self; NSString *expected = key;
  _artworkTask = [_session dataTaskWithRequest:request completionHandler:^(NSData *data, NSURLResponse *response, NSError *error) {
    PorticoSystemMedia *strong = weakSelf; if (!strong) return;
    if (error || [(NSHTTPURLResponse *)response statusCode] != 200 || data.length == 0 || data.length > 4 * 1024 * 1024) return;
    CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)data, (__bridge CFDictionaryRef)@{(__bridge id)kCGImageSourceShouldCache: @NO});
    if (!source) return;
    CGImageRef image = CGImageSourceCreateThumbnailAtIndex(source, 0, (__bridge CFDictionaryRef)@{(__bridge id)kCGImageSourceCreateThumbnailFromImageAlways: @YES, (__bridge id)kCGImageSourceThumbnailMaxPixelSize: @600, (__bridge id)kCGImageSourceCreateThumbnailWithTransform: @YES});
    CFRelease(source); if (!image) return;
    UIImage *thumbnail = [UIImage imageWithCGImage:image]; CGImageRelease(image);
    MPMediaItemArtwork *artwork = [[MPMediaItemArtwork alloc] initWithBoundsSize:thumbnail.size requestHandler:^UIImage *(CGSize size) { return thumbnail; }];
    dispatch_async(dispatch_get_main_queue(), ^{
      if (![strong->_artworkKey isEqual:expected]) return;
      strong->_artworkTask = nil; strong->_artwork = artwork; [strong publish];
    });
  }];
  [_artworkTask resume];
}
// The bearer must never follow a redirect to somewhere else.
- (void)URLSession:(NSURLSession *)session didReceiveChallenge:(NSURLAuthenticationChallenge *)challenge completionHandler:(void (^)(NSURLSessionAuthChallengeDisposition, NSURLCredential *))completionHandler {[PorticoServerTrust handleChallenge:challenge completion:completionHandler];}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest *))completionHandler { completionHandler(nil); }

#pragma mark - Remote commands

- (void)add:(MPRemoteCommand *)command handler:(MPRemoteCommandHandlerStatus (^)(MPRemoteCommandEvent *event))handler {
  command.enabled = YES;
  id target = [command addTargetWithHandler:handler];
  [_targets addObject:@{@"command": command, @"target": target}];
}
- (void)registerCommands {
  MPRemoteCommandCenter *center = MPRemoteCommandCenter.sharedCommandCenter;
  // Re-register each time: skip intervals, live state and next/previous availability change.
  [self unregisterCommands];
  __weak PorticoSystemMedia *weakSelf = self;
  BOOL live = [_info[@"live"] boolValue];
  [self add:center.playCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf command:@{@"command": @"play"}]; }];
  [self add:center.pauseCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf command:@{@"command": @"pause"}]; }];
  [self add:center.togglePlayPauseCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf command:@{@"command": @"toggle"}]; }];
  [self add:center.stopCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf command:@{@"command": @"pause"}]; }];
  if (!live) {
    center.skipForwardCommand.preferredIntervals = @[_info[@"skipForwardSeconds"]];
    center.skipBackwardCommand.preferredIntervals = @[_info[@"skipBackSeconds"]];
    [self add:center.skipForwardCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf skip:YES event:e]; }];
    [self add:center.skipBackwardCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf skip:NO event:e]; }];
    [self add:center.changePlaybackPositionCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) {
      if (![e isKindOfClass:MPChangePlaybackPositionCommandEvent.class]) return MPRemoteCommandHandlerStatusCommandFailed;
      return [weakSelf command:@{@"command": @"seek", @"positionSeconds": @(MAX(0, ((MPChangePlaybackPositionCommandEvent *)e).positionTime))}];
    }];
    if ([_info[@"canNext"] boolValue] || _chapters.count) [self add:center.nextTrackCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf step:YES]; }];
    else center.nextTrackCommand.enabled = NO;
    if ([_info[@"canPrevious"] boolValue] || _chapters.count) [self add:center.previousTrackCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *e) { return [weakSelf step:NO]; }];
    else center.previousTrackCommand.enabled = NO;
  } else {
    for (MPRemoteCommand *c in @[center.skipForwardCommand, center.skipBackwardCommand, center.changePlaybackPositionCommand, center.nextTrackCommand, center.previousTrackCommand]) c.enabled = NO;
  }
  center.changePlaybackRateCommand.enabled = NO; center.seekForwardCommand.enabled = NO; center.seekBackwardCommand.enabled = NO;
  _registered = YES;
}
- (void)unregisterCommands {
  for (NSDictionary *entry in _targets) { MPRemoteCommand *c = entry[@"command"]; [c removeTarget:entry[@"target"]]; c.enabled = NO; }
  [_targets removeAllObjects]; _registered = NO; _commandSignature = nil;
}
- (MPRemoteCommandHandlerStatus)command:(NSDictionary *)body {
  if (!_active) return MPRemoteCommandHandlerStatusNoActionableNowPlayingItem;
  if (!_listening) return MPRemoteCommandHandlerStatusCommandFailed;
  NSMutableDictionary *event = [body mutableCopy]; event[@"itemId"] = _info[@"itemId"]; if (!event[@"reason"]) event[@"reason"] = @"remote";
  // The command belongs to the exact published source; JavaScript refuses a missing or older token.
  if (_info[@"sourceToken"]) event[@"sourceToken"] = _info[@"sourceToken"];
  [self send:@"PorticoSystemMediaCommand" body:event];
  return MPRemoteCommandHandlerStatusSuccess;
}
/** Skip forward inside an intro, recap or credits marker jumps to its end, like the on-screen
 * "Skip Intro" button, so an AirPods double-tap does the obvious thing. */
- (MPRemoteCommandHandlerStatus)skip:(BOOL)forward event:(MPRemoteCommandEvent *)e {
  double interval = [e isKindOfClass:MPSkipIntervalCommandEvent.class] ? ((MPSkipIntervalCommandEvent *)e).interval : [_info[forward ? @"skipForwardSeconds" : @"skipBackSeconds"] doubleValue];
  double position = [self position];
  if (forward) for (NSDictionary *m in _markers) {
    if ([@[@"intro", @"recap", @"credits", @"outro", @"commercial"] containsObject:m[@"kind"]] && [m[@"startSeconds"] doubleValue] <= position && [m[@"endSeconds"] doubleValue] > position + 1)
      return [self command:@{@"command": @"seek", @"positionSeconds": m[@"endSeconds"], @"marker": m[@"kind"]}];
  }
  return [self command:@{@"command": @"skip", @"deltaSeconds": @(forward ? interval : -interval)}];
}
/** Next/previous: the queue when there is one, otherwise chapters. */
- (MPRemoteCommandHandlerStatus)step:(BOOL)forward {
  double position = [self position];
  if (forward) {
    if ([_info[@"canNext"] boolValue]) return [self command:@{@"command": @"next"}];
    for (NSDictionary *c in _chapters) if ([c[@"startSeconds"] doubleValue] > position + 1) return [self command:@{@"command": @"seek", @"positionSeconds": c[@"startSeconds"], @"chapter": c[@"title"]}];
    return MPRemoteCommandHandlerStatusNoSuchContent;
  }
  if ([_info[@"canPrevious"] boolValue] && position < 5) return [self command:@{@"command": @"previous"}];
  double target = 0;
  for (NSDictionary *c in _chapters) if ([c[@"startSeconds"] doubleValue] < position - 3) target = [c[@"startSeconds"] doubleValue];
  return [self command:@{@"command": @"seek", @"positionSeconds": @(target)}];
}

#pragma mark - Audio session events (video only)

- (void)interruption:(NSNotification *)note {
  dispatch_async(dispatch_get_main_queue(), ^{
    if (!self->_active || [self controller].listeningActive) return;
    NSUInteger type = [note.userInfo[AVAudioSessionInterruptionTypeKey] unsignedIntegerValue];
    if (type == AVAudioSessionInterruptionTypeBegan) {
      // Siri, a call or an alarm: the system has already paused the player; make the player's
      // intent agree so it doesn't fight to resume.
      self->_interrupted = YES; [self command:@{@"command": @"pause", @"reason": @"interruption"}];
    } else if (self->_interrupted) {
      self->_interrupted = NO;
      BOOL resume = ([note.userInfo[AVAudioSessionInterruptionOptionKey] unsignedIntegerValue] & AVAudioSessionInterruptionOptionShouldResume) != 0;
      if (resume) [self command:@{@"command": @"play", @"reason": @"interruption"}];
    }
  });
}
- (void)routeChanged:(NSNotification *)note {
  dispatch_async(dispatch_get_main_queue(), ^{
    [self updateRoute];
    if (!self->_active || [self controller].listeningActive) return;
    // Headphones unplugged or AirPods taken out: pause, as every Apple player does.
    if ([note.userInfo[AVAudioSessionRouteChangeReasonKey] unsignedIntegerValue] == AVAudioSessionRouteChangeReasonOldDeviceUnavailable)
      [self command:@{@"command": @"pause", @"reason": @"route"}];
  });
}

- (void)invalidate {
  [NSNotificationCenter.defaultCenter removeObserver:self];
  dispatch_async(dispatch_get_main_queue(), ^{
    [self clearVideo];
    AVPlayer *old = self->_player;
    if (old) { @try { [old removeObserver:self forKeyPath:@"rate"]; [old removeObserver:self forKeyPath:@"externalPlaybackActive"]; } @catch (NSException *e) {} }
    self->_player = nil; [self->_session invalidateAndCancel]; self->_session = nil;
  });
  [super invalidate];
}
@end
