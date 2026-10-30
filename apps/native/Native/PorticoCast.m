#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>
#import <GoogleCast/GoogleCast.h>

/**
 * IOS-PLAYER-03: Google Cast, as a transport only. The receiver is Portico's own page
 * (cast.getportico.tv); this module finds a Cast device (the SDK's own picker), opens a session,
 * and carries JSON on Portico's channel both ways. It never sees a media address or a token: the
 * JavaScript protocol pairs the television with the server by a one-use code, and the television
 * fetches everything itself.
 *
 * Available only when the app was built with a receiver application id (AppDelegate sets up the
 * Cast context); otherwise `available()` answers NO and no Cast row is offered.
 */
static NSString *const PorticoCastNamespace = @"urn:x-cast:tv.getportico.cast";

/** One delegate per attachment: a queued callback retains its original witness. */
@interface PorticoCastAttachmentDelegate : NSObject <GCKGenericChannelDelegate>
@property(nonatomic, copy) void (^receive)(GCKGenericChannel *, NSString *, NSString *);
@end
@implementation PorticoCastAttachmentDelegate
- (void)castChannel:(GCKGenericChannel *)channel didReceiveTextMessage:(NSString *)message withNamespace:(NSString *)protocolNamespace {
  if (self.receive) self.receive(channel, message, protocolNamespace);
}
@end

@interface PorticoCast : RCTEventEmitter <RCTBridgeModule, GCKSessionManagerListener>
@end

@implementation PorticoCast {
  GCKGenericChannel *_channel;
  __weak GCKCastSession *_channelSession;
  PorticoCastAttachmentDelegate *_channelDelegate;
  NSString *_attachmentId;
  BOOL _listening;
}
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (NSArray<NSString *> *)supportedEvents { return @[@"PorticoCastSession", @"PorticoCastMessage"]; }

/** The receiver application this build was made for, so the app can tell whether the server names the same one. Empty when the build has none. */
- (NSDictionary *)constantsToExport {
  NSString *application = [NSBundle.mainBundle objectForInfoDictionaryKey:@"PorticoCastApplicationID"];
  BOOL named = [application isKindOfClass:NSString.class] && application.length > 0 && ![application hasPrefix:@"$("];
  return @{@"applicationId": named ? application : @""};
}

- (void)startObserving {
  _listening = YES;
  if (GCKCastContext.isSharedInstanceInitialized) [GCKCastContext.sharedInstance.sessionManager addListener:self];
}
- (void)stopObserving {
  _listening = NO;
  [self detachChannel];
  if (GCKCastContext.isSharedInstanceInitialized) [GCKCastContext.sharedInstance.sessionManager removeListener:self];
}
- (void)invalidate {
  void (^cleanup)(void) = ^{ [self stopObserving]; [self detachChannel]; };
  if (NSThread.isMainThread) cleanup(); else dispatch_async(dispatch_get_main_queue(), cleanup);
  [super invalidate];
}

- (void)emitSession:(NSString *)state session:(GCKSession *)session attachment:(NSString *)attachment error:(NSError *)error {
  if (!_listening || !attachment.length) return;
  [self sendEventWithName:@"PorticoCastSession" body:@{
    @"state": state,
    @"attachmentId": attachment,
    @"deviceName": session.device.friendlyName ?: @"",
    // The SDK's own id for the television: two with one name keep separate pairing proofs.
    @"deviceId": session.device.deviceID ?: @"",
    @"error": error ? @"cast_failed" : @"",
  }];
}

- (void)detachChannel {
  GCKGenericChannel *original = _channel; GCKCastSession *session = _channelSession;
  __attribute__((objc_precise_lifetime)) PorticoCastAttachmentDelegate *delegate = _channelDelegate;
  // Retire the witness before SDK unregister can synchronously call back.
  _channel = nil; _channelSession = nil; _channelDelegate = nil; _attachmentId = nil;
  original.delegate = nil;
  if (original) [session removeChannel:original];
  (void)delegate;
}

- (BOOL)attachmentCurrent:(NSString *)attachment session:(GCKCastSession *)session channel:(GCKGenericChannel *)channel {
  return attachment.length && [_attachmentId isEqualToString:attachment] && session && _channelSession == session && channel && _channel == channel &&
    GCKCastContext.isSharedInstanceInitialized && GCKCastContext.sharedInstance.sessionManager.currentCastSession == session && session.connectionState == GCKConnectionStateConnected;
}
- (void)attach:(GCKCastSession *)session {
  if (!session || !GCKCastContext.isSharedInstanceInitialized || GCKCastContext.sharedInstance.sessionManager.currentCastSession != session || session.connectionState != GCKConnectionStateConnected) return;
  if (_channel && _channelSession == session && _attachmentId.length) return;
  [self detachChannel];
  GCKGenericChannel *channel = [[GCKGenericChannel alloc] initWithNamespace:PorticoCastNamespace];
  NSString *attachment = NSUUID.UUID.UUIDString;
  PorticoCastAttachmentDelegate *delegate = [PorticoCastAttachmentDelegate new];
  __weak PorticoCast *weakSelf = self;
  __weak GCKCastSession *originalSession = session;
  delegate.receive = ^(GCKGenericChannel *callbackChannel, NSString *message, NSString *protocolNamespace) {
    GCKCastSession *capturedSession = originalSession;
    void (^deliver)(void) = ^{
      PorticoCast *owner = weakSelf;
      if (!owner || !owner->_listening || ![owner attachmentCurrent:attachment session:capturedSession channel:callbackChannel] ||
          ![protocolNamespace isEqualToString:PorticoCastNamespace] || [message lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 65536) return;
      [owner sendEventWithName:@"PorticoCastMessage" body:@{@"message":message, @"attachmentId":attachment}];
    };
    if (NSThread.isMainThread) deliver(); else dispatch_async(dispatch_get_main_queue(), deliver);
  };
  channel.delegate = delegate;
  if ([session addChannel:channel]) { _channel = channel; _channelSession = session; _channelDelegate = delegate; _attachmentId = attachment; }
  else channel.delegate = nil;
}

#pragma mark Session listener

- (void)sessionManager:(GCKSessionManager *)manager didStartCastSession:(GCKCastSession *)session {
  [self attach:session];
  if ([self attachmentCurrent:_attachmentId session:session channel:_channel]) [self emitSession:@"started" session:session attachment:_attachmentId error:nil];
}
- (void)sessionManager:(GCKSessionManager *)manager didResumeCastSession:(GCKCastSession *)session {
  [self attach:session];
  if ([self attachmentCurrent:_attachmentId session:session channel:_channel]) [self emitSession:@"started" session:session attachment:_attachmentId error:nil];
}
- (void)finishSession:(GCKCastSession *)session state:(NSString *)state error:(NSError *)error {
  if (!session || session.connectionState == GCKConnectionStateConnected || _channelSession != session || !_attachmentId.length) return;
  GCKCastSession *current = GCKCastContext.isSharedInstanceInitialized ? GCKCastContext.sharedInstance.sessionManager.currentCastSession : nil;
  if (current && current != session) return;
  NSString *attachment = _attachmentId;
  [self detachChannel];
  [self emitSession:state session:session attachment:attachment error:error];
}
- (void)sessionManager:(GCKSessionManager *)manager didEndCastSession:(GCKCastSession *)session withError:(NSError *)error {
  [self finishSession:session state:@"ended" error:error];
}
- (void)sessionManager:(GCKSessionManager *)manager didFailToStartCastSession:(GCKCastSession *)session withError:(NSError *)error {
  [self finishSession:session state:@"failed" error:error];
}
- (void)sessionManager:(GCKSessionManager *)manager didSuspendCastSession:(GCKCastSession *)session withReason:(GCKConnectionSuspendReason)reason {
  [self finishSession:session state:@"ended" error:nil];
}

#pragma mark JavaScript

RCT_REMAP_METHOD(available, availableWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  resolve(@(GCKCastContext.isSharedInstanceInitialized));
}

/** The current session, if one is connected (after an app relaunch the SDK may resume it). */
RCT_REMAP_METHOD(current, currentWithResolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  if (!_listening || !GCKCastContext.isSharedInstanceInitialized) { resolve([NSNull null]); return; }
  GCKCastSession *session = GCKCastContext.sharedInstance.sessionManager.currentCastSession;
  if (!session || session.connectionState != GCKConnectionStateConnected) { resolve([NSNull null]); return; }
  [self attach:session];
  if (![self attachmentCurrent:_attachmentId session:session channel:_channel]) { resolve([NSNull null]); return; }
  resolve(@{@"attachmentId":_attachmentId,@"deviceName": session.device.friendlyName ?: @"", @"deviceId": session.device.deviceID ?: @""});
}

/** The SDK's own device chooser; the session events report the outcome. */
RCT_EXPORT_METHOD(choose) {
  if (!GCKCastContext.isSharedInstanceInitialized) return;
  [GCKCastContext.sharedInstance presentCastDialog];
}

RCT_REMAP_METHOD(send, send:(NSString *)json attachment:(NSString *)attachment resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  GCKGenericChannel *channel = _channel; GCKCastSession *session = _channelSession;
  if (![self attachmentCurrent:attachment session:session channel:channel] || [json lengthOfBytesUsingEncoding:NSUTF8StringEncoding] > 65536) { reject(@"cast_not_connected", @"No Cast session.", nil); return; }
  NSError *error = nil;
  if ([channel sendTextMessage:json error:&error]) resolve(@YES);
  else reject(@"cast_send_failed", @"The message was not sent.", error);
}

RCT_EXPORT_METHOD(end:(BOOL)stopReceiver attachment:(NSString *)attachment) {
  GCKCastSession *session = _channelSession; GCKGenericChannel *channel = _channel;
  if (![self attachmentCurrent:attachment session:session channel:channel]) return;
  [GCKCastContext.sharedInstance.sessionManager endSessionAndStopCasting:stopReceiver];
}
@end
