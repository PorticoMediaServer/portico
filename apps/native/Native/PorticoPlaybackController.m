#import "PorticoPlaybackController.h"
#import "PorticoPlaybackFailures.h"
#import "PorticoServerTrust.h"
#import "PorticoAudioSelection.h"
#import "PorticoAudioRender.h"
#import "PorticoSubtitleSelection.h"
#import "PorticoSubtitleOverlay.h"
#import "PorticoPlaybackObservation.h"
#import "PorticoPresentationReadiness.h"
#import <UIKit/UIKit.h>
#import <AVKit/AVKit.h>
#import <sys/utsname.h>
#import <errno.h>
/** Device-fit settings for every video item (weak devices; not quality: the viewer's quality
 * choice stays with the server plan).
 * - iPhone/iPad: never pick an adaptive variant larger than the screen (a 4K rung on a 1080p
 *   phone costs bandwidth, decode power and battery for nothing).
 * - Apple TV HD and other 2 GB devices: cap the forward buffer at 30 s so a high-bitrate stream
 *   can't take memory the UI needs; newer devices keep AVPlayer's automatic buffering. */
/** OS-07: a downloaded title plays in this controller like a streamed one. A file source is
 * accepted only inside the app's own Downloads folder (PorticoDownloads' root), resolved and
 * checked after symlinks, and never from anywhere else on the device. */
static BOOL PorticoAllowedLocalSource(NSURL *url) {
 if(!url.isFileURL||url.query.length||url.fragment.length)return NO;
 NSURL *base=[NSFileManager.defaultManager URLsForDirectory:NSApplicationSupportDirectory inDomains:NSUserDomainMask].firstObject;
 if(!base)return NO;
 NSString *root=[[[base URLByAppendingPathComponent:@"Downloads" isDirectory:YES] URLByResolvingSymlinksInPath].path stringByStandardizingPath];
 NSString *path=[[url URLByResolvingSymlinksInPath].path stringByStandardizingPath];
 BOOL directory=NO;
 return root.length&&[path hasPrefix:[root stringByAppendingString:@"/"]]&&[NSFileManager.defaultManager fileExistsAtPath:path isDirectory:&directory]&&!directory;
}
static BOOL PorticoPlaybackNumber(id value) {
 return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)!=CFBooleanGetTypeID()&&isfinite([value doubleValue]);
}
static BOOL PorticoPlaybackInteger(id value,double minimum) {
 return PorticoPlaybackNumber(value)&&[value doubleValue]>=minimum&&[value doubleValue]<=9007199254740991.0&&floor([value doubleValue])==[value doubleValue];
}
static BOOL PorticoAllowedSource(NSURL *url) {
 if([@[@"http",@"https"] containsObject:url.scheme.lowercaseString])return url.host.length>0&&!url.user&&!url.password;
 return PorticoAllowedLocalSource(url);
}

static void PorticoConfigureItem(AVPlayerItem *item) {
 static double memoryGB; static CGSize screenPixels; static dispatch_once_t once;
 dispatch_once(&once, ^{
  memoryGB = NSProcessInfo.processInfo.physicalMemory / 1073741824.0;
#if !TARGET_OS_TV
  CGSize native = UIScreen.mainScreen.nativeBounds.size; screenPixels = CGSizeMake(MAX(native.width, native.height), MIN(native.width, native.height));
#endif
 });
 if (memoryGB > 0 && memoryGB <= 2.5) item.preferredForwardBufferDuration = 30;
#if !TARGET_OS_TV
 if (screenPixels.width > 0) item.preferredMaximumResolution = screenPixels;
#endif
}
@interface PorticoPlaybackController ()
@property(nonatomic,copy) NSDictionary *command;
@end
@implementation PorticoPlaybackController {
 NSString *_listeningOwner; NSDictionary *_listeningAttachment; NSDictionary *_listeningState;
 NSInteger _listeningRouteRevision; BOOL _listeningRouteReady; NSDictionary *_listeningRender; NSString *_listeningOrigin; NSDictionary *_listeningSeek; BOOL _listeningAllowed; BOOL _applyingListening;
 PorticoAudioRender *_audioRender;
 PorticoSubtitleOverlay *_subtitleOverlay;
 AVPlayer *_player; id _timeObserver; id _subtitleTimeObserver; PorticoAudioSelection *_audioSelection; PorticoLocalAudioSelection *_localAudioSelection; PorticoSubtitleSelection *_subtitleSelection; NSString *_subtitleText;NSDictionary *_subtitleCue;
 NSDictionary *_binding; NSString *_lease; NSString *_owner; NSInteger _commandRevision; NSInteger _factSequence;
 BOOL _hasPlayed;double _previousProgress;
 BOOL _routeSuspended; BOOL _routeResume; BOOL _routeSeeking; BOOL _routeFailed; NSInteger _routeEpoch; double _routePosition;
 NSInteger _acceptedSeekRevision; double _acceptedSeekTarget; BOOL _seekRetired; BOOL _invalidated;
 NSString *_itemInstanceId; NSString *_source; NSString *_instanceId; NSInteger _intentId; NSInteger _seekRevision; BOOL _paused; BOOL _observing; BOOL _seeking; NSNumber *_timelineOriginMS;
 // The item whose status is observed, held so the observer always comes off the item it went on.
 AVPlayerItem *_observedItem;
 AVPlayerItemVideoOutput *_videoOutput; CVPixelBufferRef _capturedVideoFrame; CMTime _capturedVideoTime;
 // Portico's caption style from the subtitle command in force.
 NSDictionary *_subtitleStyle;
 // Television: the display is being asked to match the title before it is announced ready, so
 // the opening seconds are not played into a screen that is still changing mode.
 BOOL _displayWaiting; NSInteger _displayEpoch; __weak AVPlayerItem *_displayMatchedItem; id _displayStartObserver; id _displayEndObserver;
 // Only the exact criteria installed by this controller may be withdrawn.
#if TARGET_OS_TV
 AVDisplayCriteria *_ownedDisplayCriteria; __weak AVDisplayManager *_ownedDisplayManager;
#endif
 // This controller activated the audio session for a video and has not yet given it back.
 BOOL _videoSessionActive;
#if DEBUG
 BOOL _testSeekInjected; BOOL _testForceClamp;
#endif
}
/** KVO context for the AirPlay route (`externalPlaybackActive`, SUP-007). */
static void *PorticoExternalPlaybackContext=&PorticoExternalPlaybackContext;
- (instancetype)init {
 if((self=[super init])){_listeningRouteReady=YES;_previousProgress=NAN;_paused=YES;_seekRevision=-1;_acceptedSeekRevision=-1;_instanceId=NSUUID.UUID.UUIDString;_player=[AVPlayer new];
 [_player addObserver:self forKeyPath:@"externalPlaybackActive" options:0 context:PorticoExternalPlaybackContext];
 __weak PorticoPlaybackController *weakSelf=self;
 _audioRender=[PorticoAudioRender new];_audioRender.onFact=^(NSDictionary *renderFact){
  PorticoPlaybackController *owner=weakSelf;if(!owner||!owner->_audioRender.active||!owner->_source.length)return;
  NSMutableDictionary *fact=[renderFact mutableCopy];fact[@"intentId"]=@(owner->_intentId);fact[@"instanceId"]=owner->_instanceId;[owner publish:fact];
 };
 _audioSelection=[PorticoAudioSelection new];_audioSelection.onFact=^(NSDictionary *audioFact){
  PorticoPlaybackController *view=weakSelf;if(!view||!view.onFact||!view->_source.length)return;
  if([audioFact[@"audioBinding"][@"intentId"]integerValue]!=view->_intentId)return;
  NSMutableDictionary *fact=[audioFact mutableCopy];fact[@"intentId"]=@(view->_intentId);fact[@"instanceId"]=view->_instanceId;
  double position=[view sourcePosition];fact[@"position"]=@(isfinite(position)?position:0);fact[@"paused"]=@((BOOL)(view->_player.timeControlStatus==AVPlayerTimeControlStatusPaused&&!view->_seeking&&!view->_routeResume));[fact addEntriesFromDictionary:[view rateFacts]];[view publish:fact];
 };
 _localAudioSelection=[PorticoLocalAudioSelection new];_localAudioSelection.onFact=^(NSDictionary *localFact){
  PorticoPlaybackController *view=weakSelf;if(!view||!view->_source.length||![NSURL URLWithString:view->_source].isFileURL)return;
  NSMutableDictionary *fact=[localFact mutableCopy];fact[@"instanceId"]=view->_instanceId;fact[@"intentId"]=@(view->_intentId);
  double position=[view sourcePosition];fact[@"position"]=@(isfinite(position)?position:0);[view publish:fact];
 };
 _subtitleOverlay=[PorticoSubtitleOverlay new];_subtitleOverlay.onStatus=^(NSString *error){
  PorticoPlaybackController *view=weakSelf;if(!view)return;[view emit:@"subtitleStatus" error:error];
  if(error.length&&[NSURL URLWithString:view->_source].isFileURL&&!view->_subtitleOverlay.active){
   NSDictionary *subtitles=[view scopedAudio:view->_command[@"subtitles"] binding:view->_binding];
   if([subtitles[@"documentUrl"]isKindOfClass:NSString.class])[view->_subtitleSelection apply:subtitles item:view->_player.currentItem source:view->_source];
  }
 };
 _subtitleOverlay.allowsLocalFile=^BOOL(NSURL *url){return PorticoAllowedLocalSource(url);};
 _subtitleSelection=[PorticoSubtitleSelection new];_subtitleText=@"";
 _subtitleSelection.allowsLocalFile=^BOOL(NSURL *url){return PorticoAllowedLocalSource(url);};
 _subtitleSelection.onText=^(NSDictionary *cue){PorticoPlaybackController *view=weakSelf;if(!view)return;view->_subtitleCue=[cue copy];view->_subtitleText=[cue[@"text"] copy];[[NSNotificationCenter defaultCenter]postNotificationName:@"PorticoPlaybackSubtitleChanged" object:view];};
 _subtitleSelection.onStatus=^(NSString *error){[weakSelf emit:@"subtitleStatus" error:error];};
 _subtitleTimeObserver=[_player addPeriodicTimeObserverForInterval:CMTimeMake(1,20) queue:dispatch_get_main_queue() usingBlock:^(CMTime time){PorticoPlaybackController *view=weakSelf;if(view)[view->_subtitleSelection tick:time];}];
 _timeObserver=[_player addPeriodicTimeObserverForInterval:CMTimeMake(1,2) queue:dispatch_get_main_queue() usingBlock:^(CMTime time){[weakSelf applyRouteResume];[weakSelf applyPendingSeek];[weakSelf emit:@"progress" error:nil];}];
 [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(ended:) name:AVPlayerItemDidPlayToEndTimeNotification object:nil];
 // An item can stop without its status ever leaving ReadyToPlay: a stall when the buffer runs
 // dry, and a failure part-way through. Neither was observed, so a mid-film failure showed
 // nothing at all, not even a spinner.
 [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(stalled:) name:AVPlayerItemPlaybackStalledNotification object:nil];
 [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(failedToEnd:) name:AVPlayerItemFailedToPlayToEndTimeNotification object:nil];
 [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(becameActive) name:UIApplicationDidBecomeActiveNotification object:nil];
 }return self;
}
- (void)displayReadyForLease:(NSString *)lease {if([_lease isEqual:lease]){_hasPlayed=YES;[self emit:@"firstFrame" error:nil];}}
- (NSDictionary *)testStaleCommands {
#if DEBUG
 if(![NSProcessInfo.processInfo.environment[@"PORTICO_TEST_PLAYER_OWNERSHIP"] isEqualToString:@"1"]||!_command||_acceptedSeekRevision<1)return @{@"available":@NO};
 NSDictionary *before=_command;NSInteger revision=_commandRevision;NSString *item=_itemInstanceId;BOOL paused=_paused;double rate=[self desiredRate];
 NSMutableDictionary *trial=[before mutableCopy];trial[@"commandRevision"]=@(revision-1);trial[@"pendingSeek"]=NSNull.null;
 BOOL staleCommand=[self acceptCommand:trial]!=nil;
 trial=[before mutableCopy];trial[@"commandRevision"]=@(revision+1);trial[@"pendingSeek"]=@{@"revision":@(_acceptedSeekRevision),@"positionSeconds":@(_acceptedSeekTarget+1)};
 BOOL conflict=[self acceptCommand:trial]!=nil;
 trial=[before mutableCopy];trial[@"commandRevision"]=@(revision+1);trial[@"pendingSeek"]=@{@"revision":@(_acceptedSeekRevision-1),@"positionSeconds":@(_acceptedSeekTarget)};
 BOOL staleSeek=[self acceptCommand:trial]!=nil;
 trial=[before mutableCopy];trial[@"commandRevision"]=@(revision+1);trial[@"paused"]=@1;
 BOOL invalidPaused=[self acceptCommand:trial]!=nil;
 BOOL duplicate=[self acceptCommand:before]==nil;
 // DEBUG only: an empty controller proves permanent teardown without loading media.
 PorticoPlaybackController *closed=[PorticoPlaybackController new];NSDictionary *emptyLease=[closed acquire:@"terminal-test"];[closed invalidate];
 BOOL terminalAcquire=emptyLease&&[closed acquire:@"late-owner"]==nil;
 BOOL terminalCommand=[closed acceptCommand:before]!=nil;
 NSMutableDictionary *wrongAudioBinding=[_binding mutableCopy];wrongAudioBinding[@"sessionId"]=@"not-current-session";
 BOOL audioMismatch=[self scopedAudio:@{@"binding":wrongAudioBinding} binding:_binding]==nil;

 return @{@"available":@YES,@"terminalAcquireRejected":@(terminalAcquire),@"terminalCommandRejected":@(terminalCommand),@"audioMismatchDiscarded":@(audioMismatch),@"staleCommandRejected":@(staleCommand),@"conflictingSeekRejected":@(conflict),@"staleSeekRejected":@(staleSeek),@"invalidPausedRejected":@(invalidPaused),@"duplicateAccepted":@(duplicate),@"unchanged":@(_commandRevision==revision&&[_command isEqual:before]&&[_itemInstanceId isEqual:item]&&_paused==paused&&[self desiredRate]==rate),@"instanceId":_instanceId,@"itemInstanceId":item?:@"",@"commandRevision":@(revision),@"monotonicSeconds":@(NSProcessInfo.processInfo.systemUptime)};
#else
 return @{@"available":@NO};
#endif
}
- (void)testClampNext {
#if DEBUG
 if([NSProcessInfo.processInfo.environment[@"PORTICO_TEST_PLAYER_OWNERSHIP"] isEqualToString:@"1"])_testForceClamp=YES;
#endif
}
static __weak PorticoPlaybackController *PorticoSharedPlaybackController;
+ (PorticoPlaybackController *)shared { return PorticoSharedPlaybackController; }
+ (void)setShared:(PorticoPlaybackController *)controller { PorticoSharedPlaybackController = controller; }
- (BOOL)listeningActive { return _listeningOwner != nil; }
- (double)currentSourcePosition { return [self sourcePosition]; }
- (BOOL)subtitleOverlayActive { return _subtitleOverlay.active||_subtitleSelection.overlayActive; }
- (NSDictionary *)externalPresentationBinding {
 if(!_lease.length||![_binding[@"lease"] isEqual:_lease]||_invalidated||_audioRender.active||_displayWaiting||_routeSuspended||_routeResume||_routeSeeking||_routeFailed||_seeking||[_command[@"pendingSeek"] isKindOfClass:NSDictionary.class]||!_player.currentItem||_player.currentItem.status!=AVPlayerItemStatusReadyToPlay||_observedItem!=_player.currentItem)return nil;
 return [_binding copy];
}
- (void)clearCapturedVideoFrame {
 if(_capturedVideoFrame){CVPixelBufferRelease(_capturedVideoFrame);_capturedVideoFrame=NULL;}
 _capturedVideoTime=kCMTimeInvalid;
}
- (CVPixelBufferRef)copyVideoFrameForBinding:(NSDictionary *)binding displayTime:(CMTime *)displayTime transform:(CGAffineTransform *)transform {
 if(!NSThread.isMainThread||![binding isEqual:self.externalPresentationBinding]||!_videoOutput)return NULL;
 AVPlayerItem *item=_observedItem;CMTime now=item.currentTime,actual=kCMTimeInvalid;
 if(!CMTIME_IS_NUMERIC(now))return NULL;
 CVPixelBufferRef frame=[_videoOutput copyPixelBufferForItemTime:now itemTimeForDisplay:&actual];
 if(frame){size_t width=CVPixelBufferGetWidth(frame),height=CVPixelBufferGetHeight(frame);if(!width||!height||width>8192||height>8192||width*height>16777216){CVPixelBufferRelease(frame);[self clearCapturedVideoFrame];return NULL;}}
 if(frame){[self clearCapturedVideoFrame];_capturedVideoFrame=frame;_capturedVideoTime=actual;}
 // A paused frame may have already been consumed by a previous request. Reuse only the
 // actual decoded buffer from this same item/binding and within one frame's coarse budget.
 if(!_capturedVideoFrame||!CMTIME_IS_NUMERIC(_capturedVideoTime)||fabs(CMTimeGetSeconds(CMTimeSubtract(now,_capturedVideoTime)))>.25||item!=_player.currentItem||![binding isEqual:self.externalPresentationBinding])return NULL;
 if(displayTime)*displayTime=_capturedVideoTime;
 if(transform){*transform=CGAffineTransformIdentity;for(AVPlayerItemTrack *track in item.tracks)if([track.assetTrack.mediaType isEqual:AVMediaTypeVideo]){*transform=track.assetTrack.preferredTransform;break;}}
 return CVPixelBufferRetain(_capturedVideoFrame);
}
- (PorticoSubtitleOverlay *)subtitleOverlay { return _subtitleOverlay; }
- (AVPlayer *)player { return _player; }
- (NSString *)lease { return _lease; }
- (NSDictionary *)subtitleCue {return _subtitleCue?:@{};}
- (NSDictionary *)subtitleStyle {return _subtitleStyle?:@{};}
- (NSString *)subtitleText { return _subtitleText?:@""; }
- (void)invalidate {_invalidated=YES;self.onFact=nil;[self releaseLease:_lease];}
- (NSDictionary *)acquire:(NSString *)owner {
 if(_invalidated)return nil;
 if(![owner isKindOfClass:NSString.class]||owner.length<1||owner.length>128)return nil;
 if(_lease && ![_owner isEqual:owner]) return nil;
 if(!_lease){_owner=[owner copy];_lease=NSUUID.UUID.UUIDString;_commandRevision=0;_factSequence=0;}
 return @{ @"lease":_lease, @"instanceId":_instanceId, @"engine":@"native" };
}
- (void)releaseLease:(NSString *)lease {
 if(![_lease isEqual:lease])return;
 [_audioRender invalidate];
 [_subtitleSelection invalidate];[_subtitleOverlay invalidate];[_audioSelection invalidate];[self removeObservation];[_player.currentItem cancelPendingSeeks];[_player pause];[_player replaceCurrentItemWithPlayerItem:nil];
 _source=nil;_binding=nil;_command=nil;_timelineOriginMS=nil;_lease=nil;_owner=nil;_seeking=NO;_routeSuspended=NO;_routeResume=NO;_routeSeeking=NO;_routeFailed=NO;_routeEpoch++;
 _subtitleStyle=nil;_displayWaiting=NO;_displayEpoch++;
#if TARGET_OS_TV
 [self endDisplayObservation];[self clearOwnedDisplayCriteria];
#endif
 [[NSNotificationCenter defaultCenter] postNotificationName:@"PorticoPlaybackAttachmentChanged" object:self];
 // The audio session a video activated is given back, so music another app was playing may
 // resume. Not while this app plays on: its own music, the next title, or the other engine.
 if(_videoSessionActive){
  _videoSessionActive=NO;__weak PorticoPlaybackController *weakSelf=self;
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(0.5*NSEC_PER_SEC)),dispatch_get_main_queue(),^{
   PorticoPlaybackController *view=weakSelf;
   if(!view||view->_lease||view->_listeningOwner||view->_audioRender.active)return;
   [[AVAudioSession sharedInstance] setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:nil];
  });
 }
}
/** The system pauses a video when the app leaves the screen without Picture in Picture or AirPlay,
 * and tells nobody. Back in front, the app still believed it was playing: the button said Pause
 * over a frozen picture and it took two presses to continue. JavaScript is told, so its intent
 * agrees with what the viewer sees. */
- (void)becameActive {
 if(_invalidated||_listeningOwner||_audioRender.active||!_lease||!_source.length||_paused||_seeking||_routeSuspended||_routeResume||_routeSeeking||_routeFailed)return;
 if([_command[@"pendingSeek"] isKindOfClass:NSDictionary.class])return;
 AVPlayerItem *item=_player.currentItem;
 if(!item||item.status!=AVPlayerItemStatusReadyToPlay||_player.timeControlStatus!=AVPlayerTimeControlStatusPaused)return;
 // A title that has reached its end is paused too, and says so with its own fact.
 double duration=CMTimeGetSeconds(item.duration),position=CMTimeGetSeconds(_player.currentTime);
 if(isfinite(duration)&&duration>0&&isfinite(position)&&position>=duration-1)return;
 [self emit:@"systemPaused" error:nil];
}
- (NSString *)acceptCommand:(NSDictionary *)command {
 if(_invalidated)return @"Playback controller is closed.";
 if(![command isKindOfClass:NSDictionary.class]||![command[@"binding"]isKindOfClass:NSDictionary.class])return @"Invalid playback binding.";
 if(command[@"matchDynamicRange"] && ![command[@"matchDynamicRange"] isEqual:@"auto"] && ![command[@"matchDynamicRange"] isEqual:@"off"])return @"Invalid dynamic range matching choice.";
 if(_listeningOwner && !_applyingListening) {
  // Delayed JS commands may attach the current visual surface, but cannot undo
  // native Pause, timers, an interruption or authority expiry.
  if(![_listeningState[@"media"] isKindOfClass:NSDictionary.class])return nil;
  if(![command[@"listeningOwner"] isEqual:_listeningOwner] || ![command[@"binding"][@"sessionId"] isEqual:_listeningState[@"playbackId"]] || ![command[@"binding"][@"generation"] isEqual:_listeningState[@"media"][@"generation"]])return nil;
  if(![_lease isEqual:command[@"binding"][@"lease"]])return @"Playback lease is no longer active.";
  [self applyListeningCommand:command];return nil;
 }
 id localTrack=command[@"localAudioTrack"];
 if(localTrack&&localTrack!=NSNull.null&&(![localTrack isKindOfClass:NSDictionary.class]||!PorticoPlaybackInteger(localTrack[@"revision"],1)||!PorticoPlaybackInteger(localTrack[@"catalogRevision"],1)||![localTrack[@"trackId"]isKindOfClass:NSString.class]||![localTrack[@"trackId"]length]||[localTrack[@"trackId"]length]>512||![command[@"source"]isKindOfClass:NSString.class]||![NSURL URLWithString:command[@"source"]].isFileURL))return @"Invalid local audio track command.";
 NSDictionary *binding=command[@"binding"];
 if(![binding isKindOfClass:NSDictionary.class]||![_lease isEqual:binding[@"lease"]])return @"Playback lease is no longer active.";
 if(![command[@"paused"] isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)command[@"paused"])!=CFBooleanGetTypeID())return @"Invalid paused flag.";
 if(![command[@"source"] isKindOfClass:NSString.class]||!PorticoPlaybackNumber(command[@"rate"]))return @"Invalid playback command.";
 for(NSString *field in @[@"intentId",@"generation"]){id value=binding[field];if(!PorticoPlaybackInteger(value,1))return @"Invalid playback binding.";}
 id value=command[@"commandRevision"];if(!PorticoPlaybackInteger(value,1))return @"Invalid playback revision.";
 NSInteger revision=[command[@"commandRevision"] integerValue];
 if(revision<_commandRevision)return @"Stale playback command.";
 if(revision==_commandRevision)return [_command isEqual:command]?nil:@"Conflicting playback command.";
 if(![binding[@"sessionId"] isKindOfClass:NSString.class]||![binding[@"itemId"] isKindOfClass:NSString.class])return @"Invalid playback binding.";
 id clock=command[@"timelineOriginMs"];BOOL linear=clock&&clock!=NSNull.null;
 if(linear&&(![clock isKindOfClass:NSNumber.class]||CFGetTypeID((__bridge CFTypeRef)clock)==CFBooleanGetTypeID()||!isfinite([clock doubleValue])||[clock doubleValue]<=0||[clock doubleValue]>9007199254740991.0||floor([clock doubleValue])!=[clock doubleValue]))return @"Invalid channel clock.";
 id connection=command[@"connectionState"],route=command[@"routeRevision"];
 if((connection&&connection!=NSNull.null&&(![@[@"ready",@"verifying"] containsObject:connection]||!PorticoPlaybackInteger(route,0)))||(route&&route!=NSNull.null&&!PorticoPlaybackInteger(route,0)))return @"Invalid route transition.";
 if(PorticoPlaybackInteger(_command[@"routeRevision"],0)&&(!PorticoPlaybackInteger(route,0)||[route doubleValue]<[_command[@"routeRevision"] doubleValue]))return @"Stale route transition.";
 if(_binding){
  NSInteger intent=[binding[@"intentId"] integerValue],old=[_binding[@"intentId"] integerValue];
  BOOL sameBinding=[_binding isEqual:binding],sameSource=[_source isEqual:command[@"source"]];
  NSURL *oldURL=[NSURL URLWithString:_source?:@""],*nextURL=[NSURL URLWithString:command[@"source"]];
  BOOL sameResource=oldURL&&nextURL&&[oldURL.path isEqual:nextURL.path]&&[(oldURL.query?:@"") isEqual:(nextURL.query?:@"")]&&!nextURL.user.length&&!nextURL.password.length&&!nextURL.fragment.length;
  BOOL routeChange=sameBinding&&sameResource&&PorticoPlaybackInteger(route,0)&&[route doubleValue]>(PorticoPlaybackInteger(_command[@"routeRevision"],0)?[_command[@"routeRevision"] doubleValue]:0);
  BOOL same=sameBinding&&(sameSource||routeChange);
  // A channel's next window, or the session planned again in place (a quality, version, audio or
  // subtitle change): a newer generation of the same playback, under the same lease and intent.
  BOOL replanned=intent==old&&[binding[@"lease"] isEqual:_binding[@"lease"]]&&[binding[@"sessionId"] isEqual:_binding[@"sessionId"]]&&[binding[@"itemId"] isEqual:_binding[@"itemId"]]&&[binding[@"generation"] longLongValue]>[_binding[@"generation"] longLongValue];
  if(intent<old||(intent==old&&!same&&!replanned))return @"Stale playback binding.";
  if(same&&((_timelineOriginMS!=nil)!=linear||linear&&![_timelineOriginMS isEqual:clock]))return @"Channel clock changed within a binding.";
 }


 id pending=command[@"pendingSeek"];
 if(pending && pending!=NSNull.null){if(![pending isKindOfClass:NSDictionary.class])return @"Invalid seek command.";for(NSString *field in @[@"revision",@"positionSeconds"]){id number=pending[field];double v=PorticoPlaybackNumber(number)?[number doubleValue]:NAN;if(!isfinite(v)||fabs(v)>9007199254740991.0||v<0&&(!linear||[field isEqual:@"revision"]))return @"Invalid seek command.";}if([pending[@"revision"] doubleValue]<1||floor([pending[@"revision"]doubleValue])!=[pending[@"revision"]doubleValue])return @"Invalid seek revision.";}
 BOOL sameBinding=[_binding isEqual:binding];
 BOOL hasPending=[pending isKindOfClass:NSDictionary.class];
 NSInteger incoming=hasPending?[pending[@"revision"] integerValue]:-1;
 double target=hasPending?[pending[@"positionSeconds"] doubleValue]:0;
 if(sameBinding&&hasPending){
  if(incoming<_acceptedSeekRevision)return @"Stale seek command.";
  if(incoming==_acceptedSeekRevision&&(_seekRetired||target!=_acceptedSeekTarget))return @"Conflicting or retired seek command.";
 }
 NSDictionary *render=[command[@"audioRender"]isKindOfClass:NSDictionary.class]?command[@"audioRender"]:nil;
 if(command[@"audioRender"]&&command[@"audioRender"]!=NSNull.null&&!render)return @"Invalid audio render command.";
 if(render){if(linear||[command[@"rate"]doubleValue]<.25||[command[@"rate"]doubleValue]>4)return @"Unsupported audio render context.";// A v1 plan names its own media (spec §18.1 amendment: opaque ids, not the session's); the
  // listening owner has already bound it to the session it plays.
  // Only the v1 owner's bindings say so; a /v2 plan keeps the plan-vs-binding check (INT T12).
  NSDictionary *renderBinding=binding;if(_applyingListening&&[render[@"opaqueBinding"] isEqual:@YES]&&[render[@"plan"] isKindOfClass:NSDictionary.class])renderBinding=@{@"sessionId":render[@"plan"][@"sessionId"]?:@"",@"generation":render[@"plan"][@"generation"]?:@0};
  NSString *error=[PorticoAudioRender validate:render source:command[@"source"] binding:renderBinding];if(error)return error;}
 // All validation precedes mutation. Equal live seek permits newer pause/rate commands.
 if(!sameBinding){_acceptedSeekRevision=-1;_seekRetired=NO;[self clearCapturedVideoFrame];}
 if(hasPending){_acceptedSeekRevision=incoming;_acceptedSeekTarget=target;_seekRetired=NO;}
 else if(_acceptedSeekRevision>=0)_seekRetired=YES;
 _timelineOriginMS=linear?[clock copy]:nil;
 _commandRevision=revision;_binding=[binding copy];[self setCommand:command];
 [[NSNotificationCenter defaultCenter] postNotificationName:@"PorticoPlaybackAttachmentChanged" object:self];
 return nil;
}
// Box logical expressions as BOOL: Objective-C &&/|| return int, which the
// React bridge otherwise exposes as 0/1 instead of the required booleans.
#if DEBUG
- (void)debugAudioConfigurationChange {[_audioRender debugConfigurationChange];}
#endif
- (NSDictionary *)listeningSnapshot {
 if(_audioRender.active){NSMutableDictionary *snapshot=[[_audioRender snapshot] mutableCopy];snapshot[@"binding"]=_binding?:NSNull.null;snapshot[@"commandRevision"]=@(_commandRevision);snapshot[@"seeking"]=@((BOOL)([snapshot[@"seeking"]boolValue]||[_command[@"pendingSeek"]isKindOfClass:NSDictionary.class]));return snapshot;}
 double position=[self sourcePosition];return @{@"binding":_binding?:NSNull.null,@"commandRevision":@(_commandRevision),@"positionSeconds":@(isfinite(position)?MAX(0,position):0),@"observed":@((BOOL)(isfinite(position)&&_player.currentItem&&_player.currentItem.status==AVPlayerItemStatusReadyToPlay)),@"ready":@((BOOL)(_player.currentItem&&_player.currentItem.status==AVPlayerItemStatusReadyToPlay)),@"seeking":@((BOOL)(_seeking||[_command[@"pendingSeek"] isKindOfClass:NSDictionary.class])),@"effectiveRate":@(_player.rate),@"supportedRates":[self supportedRates],@"rateWaiting":@((BOOL)(_player.timeControlStatus==AVPlayerTimeControlStatusWaitingToPlayAtSpecifiedRate)),@"rateError":[self rateFacts][@"rateError"],@"rendering":@NO};
}
- (void)listeningOwner:(NSString *)owner state:(NSDictionary *)state origin:(NSString *)origin allowed:(BOOL)allowed seek:(NSDictionary *)seek render:(NSDictionary *)render {
 BOOL changed=![_listeningOwner isEqual:owner];
 if(changed){[_player pause];[_audioRender pauseOutput];}
 _listeningOwner=[owner copy];_listeningState=[state copy];_listeningOrigin=[origin copy];_listeningAllowed=allowed;_listeningSeek=[seek copy];_listeningRender=[render copy];
 if(!_listeningAllowed){[_player pause];[_audioRender pauseOutput];}
 [self applyListeningCommand:nil];
}
- (void)clearListeningOwner:(NSString *)owner {
 if(![_listeningOwner isEqual:owner])return;
 [_player pause];_listeningAllowed=NO;_listeningOwner=nil;_listeningAttachment=nil;_listeningState=nil;_listeningOrigin=nil;_listeningSeek=nil;_listeningRender=nil;
 // Retire transport too: a retained pre-pause command must not restart AVPlayer.
 if(_lease)[self releaseLease:_lease];
}
- (void)resetListeningEngine:(NSString *)owner {
 if(![_listeningOwner isEqual:owner])return;
 _listeningAttachment=_binding?@{@"binding":_binding}:nil;
 [_audioRender invalidate];
 [_player pause];[_player.currentItem cancelPendingSeeks];[self removeObservation];[_audioSelection invalidate];[_subtitleSelection invalidate];[_subtitleOverlay invalidate];
 if(_timeObserver)[_player removeTimeObserver:_timeObserver];if(_subtitleTimeObserver)[_player removeTimeObserver:_subtitleTimeObserver];
 [_player removeObserver:self forKeyPath:@"externalPlaybackActive" context:PorticoExternalPlaybackContext];
 _player=[AVPlayer new];[_player addObserver:self forKeyPath:@"externalPlaybackActive" options:0 context:PorticoExternalPlaybackContext];_source=nil;_binding=nil;_command=nil;_seeking=NO;_seekRevision=-1;_acceptedSeekRevision=-1;_seekRetired=NO;
 __weak PorticoPlaybackController *weakSelf=self;
 _timeObserver=[_player addPeriodicTimeObserverForInterval:CMTimeMake(1,2) queue:dispatch_get_main_queue() usingBlock:^(CMTime time){[weakSelf applyRouteResume];[weakSelf applyPendingSeek];[weakSelf emit:@"progress" error:nil];}];
 _subtitleTimeObserver=[_player addPeriodicTimeObserverForInterval:CMTimeMake(1,20) queue:dispatch_get_main_queue() usingBlock:^(CMTime time){PorticoPlaybackController *view=weakSelf;if(view)[view->_subtitleSelection tick:time];}];
 [[NSNotificationCenter defaultCenter]postNotificationName:@"PorticoPlaybackAttachmentChanged" object:self];
}
- (void)listeningRoute:(NSString *)owner ready:(BOOL)ready revision:(NSInteger)revision {
 if(_listeningOwner&&(![_listeningOwner isEqual:owner]||revision<_listeningRouteRevision))return;
 _listeningRouteRevision=revision;_listeningRouteReady=ready;
 if(!ready){[_player pause];[_audioRender pauseOutput];}
}
- (void)applyListeningCommand:(NSDictionary *)attachment {
 if(attachment)_listeningAttachment=[attachment copy];
 else attachment=_listeningAttachment;
 if(!_lease||(!_binding&&!attachment[@"binding"])||![_listeningState[@"media"] isKindOfClass:NSDictionary.class])return;
 NSDictionary *media=_listeningState[@"media"],*desired=_listeningState[@"desired"];
 NSMutableDictionary *binding=[(_binding?:attachment[@"binding"]) mutableCopy];
 BOOL changed=![binding[@"sessionId"] isEqual:media[@"id"]]||![binding[@"generation"] isEqual:media[@"generation"]];
 if(changed){binding[@"intentId"]=@([binding[@"intentId"] integerValue]+1);binding[@"sessionId"]=media[@"id"];binding[@"generation"]=media[@"generation"];binding[@"itemId"]=desired[@"context"][@"itemId"];}
 double denominator=[desired[@"rate"][@"denominator"] doubleValue];double rate=denominator>0?[desired[@"rate"][@"numerator"] doubleValue]/denominator:1;
 NSMutableDictionary *command=[@{@"binding":binding,@"commandRevision":@(_commandRevision+1),@"source":[_listeningOrigin stringByAppendingString:media[@"streamUrl"]],@"connectionState":_listeningRouteReady?@"ready":@"verifying",@"routeRevision":@(_listeningRouteRevision),@"paused":@((BOOL)(!_listeningRouteReady||!_listeningAllowed||![desired[@"state"] isEqual:@"playing"])),@"rate":@(rate),@"timelineOriginMs":NSNull.null,@"pendingSeek":_listeningSeek?:NSNull.null,@"audioRender":_listeningRender?:NSNull.null,@"audio":attachment[@"audio"]?:_command[@"audio"]?:NSNull.null,@"subtitles":attachment[@"subtitles"]?:_command[@"subtitles"]?:NSNull.null} mutableCopy];
 if(attachment[@"matchDynamicRange"])command[@"matchDynamicRange"]=attachment[@"matchDynamicRange"];
 // Skip no-op updates so native facts do not churn the JS command barrier.
 NSMutableDictionary *before=[_command mutableCopy],*after=[command mutableCopy];[before removeObjectForKey:@"commandRevision"];[after removeObjectForKey:@"commandRevision"];
 if([before isEqual:after])return;
 _applyingListening=YES;NSString *error=[self acceptCommand:command];_applyingListening=NO;
 if(error){[_player pause];[_audioRender pauseOutput];[self publish:[@{@"type":@"error",@"error":error,@"position":@([self sourcePosition])} mutableCopy]];}
}
- (void)publish:(NSMutableDictionary *)fact {
 // The native bridge carries status codes. The common UI boundary owns catalogue copy.
 for(NSString *key in @[@"error",@"rateError"]){
  id value=fact[key];if(![value isKindOfClass:NSString.class]||![value length])continue;
  NSCharacterSet *invalid=[[NSCharacterSet characterSetWithCharactersInString:@"abcdefghijklmnopqrstuvwxyz0123456789_"] invertedSet];
  if([value length]>96||[value rangeOfCharacterFromSet:invalid].location!=NSNotFound)fact[key]=[key isEqual:@"rateError"]?@"playback_rate_unavailable":@"playback_failed";
 }

 if(!self.onFact||!_binding||!_lease||_routeSuspended||((_routeResume||_routeSeeking||_routeFailed)&&![fact[@"type"] isEqual:@"error"]))return;
 fact[@"itemInstanceId"]=_itemInstanceId?:@"";fact[@"binding"]=_binding;fact[@"factSequence"]=@(++_factSequence);fact[@"lastCommandRevision"]=@(_commandRevision);fact[@"seeking"]=@((BOOL)(_seeking||[_command[@"pendingSeek"] isKindOfClass:NSDictionary.class]));
 // Source/display/seek/route settlement is acknowledged by the actual native getter,
 // independently of ready/firstFrame and of AVKit's eventual PiP request outcome.
 fact[@"externalPresentationBinding"]=PorticoExternalPresentationBindingFact(self.externalPresentationBinding,fact[@"binding"],_lease);
 self.onFact(fact);
}
- (NSDictionary *)scopedAudio:(id)value binding:(NSDictionary *)binding {
 NSDictionary *audio=[value isKindOfClass:NSDictionary.class]?value:nil;
 NSDictionary *audioBinding=[audio[@"binding"] isKindOfClass:NSDictionary.class]?audio[@"binding"]:nil;
 for(NSString *field in @[@"intentId",@"sessionId",@"generation"]){if(![audioBinding[field] isEqual:binding[field]])return nil;}
 return audio;
}
- (void)setCommand:(NSDictionary *)command {
 BOOL sameBinding=[_command[@"binding"] isEqual:command[@"binding"]];
 BOOL verifying=[command[@"connectionState"] isEqual:@"verifying"];
 NSInteger nextId=[command[@"binding"][@"intentId"] integerValue];NSString *source=command[@"source"];
 if(!sameBinding){_routeResume=NO;_routeSeeking=NO;_routeSuspended=NO;_routeFailed=NO;_routeEpoch++;}
 BOOL changed=![_source isEqualToString:source];
 if(sameBinding&&(verifying||changed)&&!_routeResume&&!_routeSuspended){double position=_routeFailed?_routePosition:[self sourcePosition];if(isfinite(position)&&(_timelineOriginMS||position>=0)){_routePosition=position;_routeResume=YES;_routeFailed=NO;_routeEpoch++;}}
 BOOL matchingWasWaiting=_displayWaiting;
 BOOL matchingWasOff=[_command[@"matchDynamicRange"] isEqual:@"off"];
 _command=[command copy];_paused=[command[@"paused"] boolValue];
#if TARGET_OS_TV
 if([command[@"matchDynamicRange"] isEqual:@"off"]){
  // Retire pending asset callbacks before withdrawing only our app-owned request.
  _displayWaiting=NO;_displayEpoch++;[self endDisplayObservation];[self clearOwnedDisplayCriteria];
 }else if(matchingWasOff){_displayMatchedItem=nil;}
#else
 (void)matchingWasOff;(void)matchingWasWaiting;
#endif
 if(verifying){
  [_audioRender invalidate];
  if(!_routeSuspended){[_subtitleSelection invalidate];[_subtitleOverlay invalidate];[_audioSelection invalidate];[self removeObservation];[_player.currentItem cancelPendingSeeks];[_player pause];[_player replaceCurrentItemWithPlayerItem:nil];_routeSeeking=NO;_seeking=NO;}
  _routeSuspended=YES;return;
 }
 if([command[@"audioRender"]isKindOfClass:NSDictionary.class]){
  if(nextId!=_intentId||![_source isEqual:source])_itemInstanceId=NSUUID.UUID.UUIDString;
  [_subtitleSelection invalidate];[_subtitleOverlay invalidate];[_audioSelection invalidate];[self removeObservation];[_player.currentItem cancelPendingSeeks];[_player pause];[_player replaceCurrentItemWithPlayerItem:nil];
  _intentId=nextId;_source=[source copy];_seeking=NO;NSMutableDictionary *routed=[command mutableCopy];
  if(_routeResume){NSMutableDictionary *render=[command[@"audioRender"] mutableCopy];render[@"startSeconds"]=@(_routePosition);render[@"epoch"]=@([render[@"epoch"]longLongValue]+_routeEpoch);[render removeObjectForKey:@"prepared"];[render removeObjectForKey:@"committed"];routed[@"audioRender"]=render;}
  _routeSuspended=NO;_routeResume=NO;_routeSeeking=NO;[_audioRender applyCommand:routed];return;
 }
 [_audioRender invalidate];
 BOOL reload=_routeSuspended||nextId!=_intentId||changed||!_player.currentItem;_routeSuspended=NO;
 if(reload){
  _hasPlayed=NO;_previousProgress=NAN;
  _routeEpoch++;
  _displayWaiting=NO;_displayEpoch++;
#if TARGET_OS_TV
  [self endDisplayObservation];
#endif
  [_subtitleSelection invalidate];[_subtitleOverlay invalidate];[_audioSelection invalidate];[self removeObservation];[_player.currentItem cancelPendingSeeks];_seeking=NO;_routeSeeking=NO;[_player pause];[_player replaceCurrentItemWithPlayerItem:nil];_intentId=nextId;_source=[source copy];_seekRevision=-1;
  if(source.length){NSURL *url=[NSURL URLWithString:source];if(!url||!PorticoAllowedSource(url)){[self emit:@"error" error:@"playback_source_invalid"];return;}
   [self configureVideoAudioSession];
   // The owner's own certificate is answered the way every Portico connection answers it.
   AVURLAsset *asset=[AVURLAsset URLAssetWithURL:url options:nil];[asset.resourceLoader setDelegate:PorticoServerTrust.resourceLoaderDelegate queue:PorticoServerTrust.resourceLoaderQueue];
   AVPlayerItem *item=[AVPlayerItem playerItemWithAsset:asset];PorticoConfigureItem(item);
   _videoOutput=[[AVPlayerItemVideoOutput alloc]initWithPixelBufferAttributes:@{(id)kCVPixelBufferPixelFormatTypeKey:@(kCVPixelFormatType_32BGRA)}];_videoOutput.suppressesPlayerRendering=NO;[item addOutput:_videoOutput];
   _itemInstanceId=NSUUID.UUID.UUIDString;[item addObserver:self forKeyPath:@"status" options:NSKeyValueObservingOptionNew context:NULL];[item addObserver:self forKeyPath:@"seekableTimeRanges" options:NSKeyValueObservingOptionNew context:NULL];_observing=YES;_observedItem=item;[_player replaceCurrentItemWithPlayerItem:item];
   if(_routeResume){NSInteger routeEpoch=_routeEpoch;__weak PorticoPlaybackController *weakSelf=self;dispatch_after(dispatch_time(DISPATCH_TIME_NOW,10*NSEC_PER_SEC),dispatch_get_main_queue(),^{PorticoPlaybackController *view=weakSelf;if(view&&!view->_invalidated&&view->_routeEpoch==routeEpoch&&view->_player.currentItem==item&&view->_routeResume){view->_routeResume=NO;view->_routeSeeking=NO;view->_routeFailed=YES;view->_routeEpoch++;[view->_player.currentItem cancelPendingSeeks];[view->_player pause];[view emit:@"error" error:@"playback_retained_position_unavailable"];}});}
  }
 }
 NSDictionary *pending=[command[@"pendingSeek"] isKindOfClass:NSDictionary.class]?command[@"pendingSeek"]:nil;
 if(pending&&(_routeResume||_routeSeeking||_routeFailed)){_routeResume=NO;_routeSeeking=NO;_routeFailed=NO;_routeEpoch++;[_player.currentItem cancelPendingSeeks];}
 if(!pending&&_seeking){_seeking=NO;[_player.currentItem cancelPendingSeeks];}
 [_audioSelection apply:[self scopedAudio:command[@"audio"] binding:_binding] item:_player.currentItem];
 [_localAudioSelection apply:[command[@"localAudioTrack"]isKindOfClass:NSDictionary.class]?command[@"localAudioTrack"]:nil item:_player.currentItem];
 NSDictionary *subtitles=[self scopedAudio:command[@"subtitles"] binding:_binding];
 NSDictionary *style=[subtitles[@"style"] isKindOfClass:NSDictionary.class]?subtitles[@"style"]:nil;
 if(!(style==_subtitleStyle||[style isEqual:_subtitleStyle])){_subtitleStyle=[style copy];[[NSNotificationCenter defaultCenter]postNotificationName:@"PorticoPlaybackSubtitleChanged" object:self];}
 BOOL wasOverlayActive=self.subtitleOverlayActive;
 [_subtitleOverlay apply:subtitles source:_source];
 if(_subtitleOverlay.active)[_subtitleSelection apply:nil item:_player.currentItem source:_source];
 else [_subtitleSelection apply:subtitles item:_player.currentItem source:_source];
 // External AVPlayer output cannot carry our sibling UIKit caption layers. The runtime owns
 // the same-session replan; enable external playback only after that command removes them.
 _player.allowsExternalPlayback=!self.subtitleOverlayActive;
 if(wasOverlayActive!=self.subtitleOverlayActive)[NSNotificationCenter.defaultCenter postNotificationName:@"PorticoPlaybackSubtitleChanged" object:self];
#if TARGET_OS_TV
 if(!reload && _player.currentItem.status==AVPlayerItemStatusReadyToPlay && !_routeResume && !_routeSuspended && !_routeFailed){
  if(matchingWasOff && ![command[@"matchDynamicRange"] isEqual:@"off"])[self matchDisplayForItem:_player.currentItem];
  else if(matchingWasWaiting && [command[@"matchDynamicRange"] isEqual:@"off"])[self itemReady];
 }
#endif
 [self applyRouteResume];[self applyPendingSeek];[self applyIntent];
}
// A transport rebind is not a new playback intent or a user seek. Restore the
// last observed position on this exact lease/session/presentation, then resume
// the existing desired pause/rate/track state. Channel positions remain dates.
- (void)applyRouteResume {
 if(!_routeResume||_routeSuspended||_routeSeeking||_routeFailed||_player.currentItem.status!=AVPlayerItemStatusReadyToPlay)return;
 if(_timelineOriginMS&&!_player.currentItem.currentDate)return;
 _routeSeeking=YES;[_player pause];AVPlayerItem *item=_player.currentItem;NSDictionary *binding=_binding;NSInteger routeEpoch=_routeEpoch;double target=_routePosition;__weak PorticoPlaybackController *weakSelf=self;
 void (^complete)(BOOL)=^(BOOL finished){dispatch_async(dispatch_get_main_queue(),^{PorticoPlaybackController *view=weakSelf;if(!view||view->_invalidated||view->_routeEpoch!=routeEpoch||view->_player.currentItem!=item||![view->_binding isEqual:binding]||!view->_routeResume)return;view->_routeSeeking=NO;double actual=[view sourcePosition];
  if(!finished||!isfinite(actual)||fabs(actual-target)>0.5){view->_routeResume=NO;view->_routeFailed=YES;view->_routeEpoch++;[view->_player pause];[view emit:@"error" error:@"playback_retained_position_unavailable"];return;}
  view->_routeResume=NO;[view emit:@"ready" error:nil];[view applyIntent];
 });};
 if(_timelineOriginMS)[_player seekToDate:[NSDate dateWithTimeIntervalSince1970:[_timelineOriginMS doubleValue]/1000.0+target] completionHandler:complete];
 else [_player seekToTime:CMTimeMakeWithSeconds(target,600) toleranceBefore:kCMTimeZero toleranceAfter:kCMTimeZero completionHandler:complete];
}

- (void)applyPendingSeek {
 if(_audioRender.active)return;
 NSDictionary *pending=[_command[@"pendingSeek"] isKindOfClass:NSDictionary.class]?_command[@"pendingSeek"]:nil;
 if(!pending||_routeSuspended||_routeResume||_routeSeeking||_player.currentItem.status!=AVPlayerItemStatusReadyToPlay)return;
 NSInteger revision=[pending[@"revision"] integerValue];if(revision<=_seekRevision)return;
 double target=[pending[@"positionSeconds"] doubleValue];if(!isfinite(target)||(!_timelineOriginMS&&target<0))return;
 if(_timelineOriginMS&&!_player.currentItem.currentDate)return;
 _seekRevision=revision;_seeking=YES;[self clearCapturedVideoFrame];[_player pause];
 NSDictionary *capturedBinding=_binding;NSInteger intent=_intentId;AVPlayerItem *item=_player.currentItem;__weak PorticoPlaybackController *weakSelf=self;
 double seekTarget=target;
#if DEBUG
 if(_testForceClamp||(!_testSeekInjected && [NSProcessInfo.processInfo.environment[@"PORTICO_TEST_SEEK_CLAMP"] isEqualToString:@"1"])){seekTarget=target+12;_testSeekInjected=YES;_testForceClamp=NO;}
#endif
 void (^completion)(BOOL)=^(BOOL finished){
  dispatch_async(dispatch_get_main_queue(), ^{
   PorticoPlaybackController *view=weakSelf;if(!view)return;
   NSDictionary *current=[view->_command[@"pendingSeek"] isKindOfClass:NSDictionary.class]?view->_command[@"pendingSeek"]:nil;
   if(![view->_binding isEqual:capturedBinding]||view->_intentId!=intent||view->_player.currentItem!=item||view->_seekRevision!=revision||!current||[current[@"revision"] integerValue]!=revision)return;
   view->_seeking=NO;
   double observed=[view sourcePosition];
   BOOL applied=finished&&isfinite(observed)&&fabs(observed-target)<=0.5;
   if(view.onFact){NSMutableDictionary *fact=[@{@"instanceId":view->_instanceId,@"type":applied?@"seekApplied":@"seekFailed",@"intentId":@(intent),@"seekRevision":@(revision),@"position":@(isfinite(observed)?observed:0),@"paused":@((BOOL)(view->_player.timeControlStatus==AVPlayerTimeControlStatusPaused&&!view->_seeking&&!view->_routeResume)),@"error":applied?@"":@"playback_seek_unavailable"} mutableCopy];
#if DEBUG
    fact[@"requestedTarget"]=@(target);fact[@"seekFinished"]=@(finished);NSMutableArray *ranges=[NSMutableArray new];for(NSValue *value in item.seekableTimeRanges){CMTimeRange range=value.CMTimeRangeValue;double start=CMTimeGetSeconds(range.start),end=CMTimeGetSeconds(CMTimeRangeGetEnd(range));if(isfinite(start)&&isfinite(end))[ranges addObject:@[@(start),@(end)]];}fact[@"seekableRanges"]=ranges;
#endif
    [fact addEntriesFromDictionary:[view rateFacts]];[view publish:fact];
   }
  });
 };
 if(_timelineOriginMS){
  NSDate *date=[NSDate dateWithTimeIntervalSince1970:[_timelineOriginMS doubleValue]/1000.0+seekTarget];
  [_player seekToDate:date completionHandler:^(BOOL finished){
   dispatch_async(dispatch_get_main_queue(), ^{
    PorticoPlaybackController *view=weakSelf;if(!view)return;
    NSDictionary *current=[view->_command[@"pendingSeek"] isKindOfClass:NSDictionary.class]?view->_command[@"pendingSeek"]:nil;
    if(![view->_binding isEqual:capturedBinding]||view->_intentId!=intent||view->_player.currentItem!=item||view->_seekRevision!=revision||!current||[current[@"revision"] integerValue]!=revision)return;
    // Date seeking can land on a nearby keyframe. Refine once using the actual
    // post-seek date/time pair, never an assumed playlist start or duration.
    double observed=[view sourcePosition],mediaTime=CMTimeGetSeconds(item.currentTime),delta=target-observed;
    double refined=mediaTime+delta;BOOL retained=NO;
    if(finished&&seekTarget==target&&isfinite(observed)&&isfinite(mediaTime)&&fabs(delta)>0.5&&fabs(delta)<=8&&isfinite(refined)){
     CMTime exact=CMTimeMakeWithSeconds(refined,60000);
     for(NSValue *value in item.seekableTimeRanges){if(CMTimeRangeContainsTime(value.CMTimeRangeValue,exact)){retained=YES;break;}}
     if(retained){[view->_player seekToTime:exact toleranceBefore:kCMTimeZero toleranceAfter:kCMTimeZero completionHandler:completion];return;}
    }
    completion(finished);
   });
  }];
 }
 else [_player seekToTime:CMTimeMakeWithSeconds(seekTarget,600) toleranceBefore:kCMTimeZero toleranceAfter:kCMTimeZero completionHandler:completion];
}
- (double)sourcePosition {
 if(_audioRender.active)return _audioRender.position;
 if(_timelineOriginMS){NSDate *date=_player.currentItem.currentDate;return date?date.timeIntervalSince1970-[_timelineOriginMS doubleValue]/1000.0:NAN;}
 return CMTimeGetSeconds(_player.currentTime);
}
- (NSArray<NSNumber *> *)supportedRates {
 if(_player.currentItem.status!=AVPlayerItemStatusReadyToPlay)return @[];
 if(_timelineOriginMS)return @[@1];
 NSArray *rates=_player.currentItem.canPlaySlowForward?@[@0.5,@0.75,@1,@1.25,@1.5,@1.75,@2]:@[@1,@1.25,@1.5,@1.75,@2];
 // Audiobooks go to 3x, as on web; the menu offers 2.5x and 3x only for them.
 return _player.currentItem.canPlayFastForward?[rates arrayByAddingObjectsFromArray:@[@2.5,@3]]:rates;
}
- (double)desiredRate {return [_command[@"rate"] isKindOfClass:NSNumber.class]?[_command[@"rate"] doubleValue]:1;}
- (NSDictionary *)rateFacts {
 BOOL ready=_player.currentItem.status==AVPlayerItemStatusReadyToPlay;double desired=[self desiredRate];
 BOOL valid=[[self supportedRates] containsObject:@(desired)];
 return @{@"effectiveRate":@(_player.rate),@"supportedRates":[self supportedRates],@"rateReady":@(ready),@"rateWaiting":@((BOOL)(_player.timeControlStatus==AVPlayerTimeControlStatusWaitingToPlayAtSpecifiedRate)),@"rateError":ready&&!valid?@"playback_rate_unavailable":@"",@"monotonicSeconds":@(NSProcessInfo.processInfo.systemUptime)};
}
- (void)applyIntent {
 if(_audioRender.active)return;
 BOOL pending=[_command[@"pendingSeek"] isKindOfClass:NSDictionary.class];
 if(_paused||_seeking||pending||_displayWaiting||_routeSuspended||_routeResume||_routeSeeking||_routeFailed||(_listeningOwner&&!_listeningAllowed))[_player pause];
 else if(_player.currentItem.status==AVPlayerItemStatusReadyToPlay){
  double rate=[self desiredRate];
  if([[self supportedRates] containsObject:@(rate)]){if(_player.rate!=rate)_player.rate=rate;}
  else [_player pause];
 }
 [self emit:@"rateStatus" error:nil];
}
- (void)observeValueForKeyPath:(NSString *)keyPath ofObject:(id)object change:(NSDictionary *)change context:(void *)context {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self observeValueForKeyPath:keyPath ofObject:object change:change context:context];});return;}
 // AirPlay video started or ended: JavaScript burns in the captions this app draws while it lasts.
 if(context==PorticoExternalPlaybackContext){if(object==_player)[self emit:@"externalPlayback" error:nil];return;}
 if(object!=_player.currentItem)return;
 // The seekable-range observation exists for the live window only. It used to fall through to
 // the status handling, so every window update announced "ready" again and re-applied intent.
 if(![keyPath isEqual:@"status"])return;
 if(_player.currentItem.status==AVPlayerItemStatusReadyToPlay){
#if TARGET_OS_TV
  // The display is matched to the title first; `itemReady` runs when the switch has ended.
  if([self matchDisplayForItem:_player.currentItem])return;
#endif
  [self itemReady];
 }
 else if(_player.currentItem.status==AVPlayerItemStatusFailed)[self publishFailure:_player.currentItem.error];
}
- (void)itemReady {[self applyRouteResume];if(!_routeResume&&!_routeFailed)[self emit:@"ready" error:nil];[self applyPendingSeek];[self applyIntent];}
#if TARGET_OS_TV
- (AVDisplayManager *)displayManager {
 UIWindow *window=self.displayWindow;
 if(!window){for(UIScene *scene in UIApplication.sharedApplication.connectedScenes){if(![scene isKindOfClass:UIWindowScene.class])continue;for(UIWindow *candidate in ((UIWindowScene *)scene).windows){if(candidate.isKeyWindow){window=candidate;break;}}if(window)break;}}
 return window.avDisplayManager;
}
- (void)endDisplayObservation {
 if(_displayStartObserver)[[NSNotificationCenter defaultCenter]removeObserver:_displayStartObserver];
 if(_displayEndObserver)[[NSNotificationCenter defaultCenter]removeObserver:_displayEndObserver];
 _displayStartObserver=nil;_displayEndObserver=nil;
}
/**
 * Asks the television to match the title's frame rate and dynamic range before the title is
 * announced ready, and holds playback until the mode switch has ended. With the system's
 * "Match Content" settings off there is nothing to wait for. A television renegotiates HDMI for
 * one to several seconds; the wait is bounded (10 s), and a title that needs no switch is held
 * for less than half a second. Music does not ask. Once per item. Returns NO when nothing is
 * waited for.
 */
// Withdraw our matching request, never another controller's criteria or OS policy.
- (void)clearOwnedDisplayCriteria {
 AVDisplayManager *manager=_ownedDisplayManager;AVDisplayCriteria *criteria=_ownedDisplayCriteria;
 _ownedDisplayManager=nil;_ownedDisplayCriteria=nil;
 if(criteria && manager.preferredDisplayCriteria==criteria)manager.preferredDisplayCriteria=nil;
}
- (BOOL)matchDisplayForItem:(AVPlayerItem *)item {
 if([_command[@"matchDynamicRange"] isEqual:@"off"])return NO;
 if(_displayWaiting&&_displayMatchedItem==item)return YES;
 if(!item||_displayMatchedItem==item||_listeningOwner||_audioRender.active)return NO;
 AVDisplayManager *manager=[self displayManager];
 if(!manager||!manager.isDisplayCriteriaMatchingEnabled){_displayMatchedItem=item;return NO;}
 _displayMatchedItem=item;_displayWaiting=YES;NSInteger epoch=++_displayEpoch;[_player pause];
 __weak PorticoPlaybackController *weakSelf=self;
 void (^finish)(void)=^{
  PorticoPlaybackController *view=weakSelf;if(!view||view->_displayEpoch!=epoch||!view->_displayWaiting)return;
  view->_displayWaiting=NO;view->_displayEpoch++;[view endDisplayObservation];
  if(view->_invalidated||view->_player.currentItem!=item||item.status!=AVPlayerItemStatusReadyToPlay)return;
  [view itemReady];
 };
 // The longest the title is held, whatever the television does.
 dispatch_after(dispatch_time(DISPATCH_TIME_NOW,10*NSEC_PER_SEC),dispatch_get_main_queue(),finish);
 [item.asset loadValuesAsynchronouslyForKeys:@[@"preferredDisplayCriteria"] completionHandler:^{dispatch_async(dispatch_get_main_queue(),^{
  PorticoPlaybackController *view=weakSelf;if(!view||view->_displayEpoch!=epoch||!view->_displayWaiting)return;
  if(view->_player.currentItem!=item){finish();return;}
  AVDisplayManager *display=[view displayManager];
  if(!display || !display.isDisplayCriteriaMatchingEnabled || [view->_command[@"matchDynamicRange"] isEqual:@"off"]){finish();return;}
  __block BOOL started=display.isDisplayModeSwitchInProgress;
  view->_displayStartObserver=[[NSNotificationCenter defaultCenter]addObserverForName:AVDisplayManagerModeSwitchStartNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note){started=YES;}];
  // The picture is steady a moment after the switch is reported over.
  view->_displayEndObserver=[[NSNotificationCenter defaultCenter]addObserverForName:AVDisplayManagerModeSwitchEndNotification object:nil queue:NSOperationQueue.mainQueue usingBlock:^(NSNotification *note){dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(0.3*NSEC_PER_SEC)),dispatch_get_main_queue(),finish);}];
  [view clearOwnedDisplayCriteria];
  AVDisplayCriteria *criteria=item.asset.preferredDisplayCriteria;
  display.preferredDisplayCriteria=criteria;
  view->_ownedDisplayManager=display;view->_ownedDisplayCriteria=display.preferredDisplayCriteria;
  // A switch, when one is needed, begins at once. None begun shortly after asking: none is coming.
  dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(0.4*NSEC_PER_SEC)),dispatch_get_main_queue(),^{
   PorticoPlaybackController *owner=weakSelf;if(!owner||owner->_displayEpoch!=epoch)return;
   AVDisplayManager *now=[owner displayManager];
   if(!started&&!now.isDisplayModeSwitchInProgress)finish();
  });
 });}];
 return YES;
}
#endif
/** One failure, classified. A network failure is weather and is retried on the same route. An
 * engine failure (the decoder or the parser refusing what it was given) carries its class and the
 * underlying code, so the app can tell the server which route failed and ask for the next one
 * instead of showing the viewer an error. */
- (void)publishFailure:(NSError *)top {
 // The item's error log gathers every transport event of its life, most of them survived. Only a
 // recent entry can be about this failure (PorticoPlaybackFailureFacts).
 AVPlayerItemErrorLogEvent *log=_player.currentItem.errorLog.events.lastObject;
 BOOL recent=log!=nil&&log.date!=nil&&fabs(log.date.timeIntervalSinceNow)<=15;
 double position=(_routeResume||_routeFailed)?_routePosition:[self sourcePosition];
 NSMutableDictionary *fact=[@{@"instanceId":_instanceId,@"type":@"error",@"intentId":@(_intentId),@"position":@(isfinite(position)?position:0),@"paused":@YES,@"hasPlayed":@(_hasPlayed)} mutableCopy];
 [fact addEntriesFromDictionary:PorticoPlaybackFailureFacts(top,recent,log.errorDomain,log.errorStatusCode,log.errorComment)];
 [fact addEntriesFromDictionary:[self rateFacts]];[self publish:fact];
}
- (void)stalled:(NSNotification *)note{if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self stalled:note];});return;}if(note.object==_player.currentItem)[self emit:@"stalled" error:nil];}
- (void)failedToEnd:(NSNotification *)note{if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self failedToEnd:note];});return;}if(note.object!=_player.currentItem)return;NSError *error=note.userInfo[AVPlayerItemFailedToPlayToEndTimeErrorKey];[self publishFailure:error?:_player.currentItem.error];}
/** Video never configured the audio session, so on iOS the default category applied: the silent
 * switch muted a film, and nothing told the system this is long-form, multichannel content. */
- (void)configureVideoAudioSession {
 AVAudioSession *audio=[AVAudioSession sharedInstance];NSError *error=nil;
#if TARGET_OS_TV
 [audio setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeMoviePlayback options:0 error:&error];
#else
 if(@available(iOS 13.0, *))[audio setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeMoviePlayback routeSharingPolicy:AVAudioSessionRouteSharingPolicyLongFormVideo options:0 error:&error];
#endif
 if(error){error=nil;[audio setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeMoviePlayback options:0 error:&error];}
 if(@available(iOS 15.0, tvOS 15.0, *))[audio setSupportsMultichannelContent:YES error:nil];
 [audio setActive:YES error:nil];
 _videoSessionActive=YES;
}
- (void)ended:(NSNotification *)note{if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self ended:note];});return;}if(note.object==_player.currentItem)[self emit:@"ended" error:nil];}
- (NSDictionary *)observedPlayback {
 NSDictionary *binding=self.externalPresentationBinding;
 if(!binding)return @{};
 // The existing capture outlet is independent of view bounds and validates item, lease,
 // intent, generation and frame age. It reuses the same decoded buffer as snapshot capture.
 CVPixelBufferRef frame=[self copyVideoFrameForBinding:binding displayTime:NULL transform:NULL];
 NSDictionary *observation=PorticoObservedDecodedVideo(frame);
 if(frame)CVPixelBufferRelease(frame);
 return [binding isEqual:self.externalPresentationBinding]?observation:@{};
}
- (void)emit:(NSString *)type error:(NSString *)error{if(_audioRender.active)return;if(!self.onFact||!_source.length)return;double position=(_routeResume||_routeFailed)?_routePosition:[self sourcePosition];if([type isEqual:@"progress"]){if(_player.rate>0&&!_seeking&&!_routeResume&&isfinite(position)&&isfinite(_previousProgress)&&position>_previousProgress+0.05)_hasPlayed=YES;_previousProgress=position;}if(_timelineOriginMS&&!isfinite(position)&&([type isEqual:@"progress"]||[type isEqual:@"ended"]))return;NSMutableDictionary *fact=[@{@"instanceId":_instanceId,@"type":type,@"intentId":@(_intentId),@"position":@(isfinite(position)?position:0),@"paused":@((BOOL)(_player.timeControlStatus==AVPlayerTimeControlStatusPaused&&!_seeking&&!_routeResume)),@"error":error?:@""} mutableCopy];if([type isEqual:@"progress"]){[self addConnectionFacts:fact position:position];fact[@"observedPlayback"]=[self observedPlayback];}if([type isEqual:@"externalPlayback"])fact[@"active"]=@(_player.externalPlaybackActive);[fact addEntriesFromDictionary:[self rateFacts]];[self publish:fact];}
/** What the player measured of the connection and how much it holds ahead of the picture, for
 * Automatic quality: the transfer rate of the stream's own downloads (a stream only; a file read
 * in place reports none), and the buffered media after the position. */
- (void)addConnectionFacts:(NSMutableDictionary *)fact position:(double)position {
 AVPlayerItem *item=_player.currentItem;if(!item||_timelineOriginMS)return;
 AVPlayerItemAccessLogEvent *event=item.accessLog.events.lastObject;
 double observed=event?event.observedBitrate:0;
 if(isfinite(observed)&&observed>0)fact[@"throughputKbps"]=@(round(observed/1000.0));
 double now=CMTimeGetSeconds(item.currentTime);
 if(isfinite(now)){for(NSValue *value in item.loadedTimeRanges){CMTimeRange range=value.CMTimeRangeValue;double start=CMTimeGetSeconds(range.start),end=CMTimeGetSeconds(CMTimeRangeGetEnd(range));if(isfinite(start)&&isfinite(end)&&start<=now+0.5&&end>now){fact[@"bufferedSeconds"]=@(end-now);break;}}}
}
- (void)removeObservation{
 [_localAudioSelection invalidate];
#if TARGET_OS_TV
 // Every retired item also retires its pending match callback and owned criteria.
 // Audio and a verifying replacement must never inherit the previous title's request.
 _displayWaiting=NO;_displayMatchedItem=nil;_displayEpoch++;
 [self endDisplayObservation];[self clearOwnedDisplayCriteria];
#endif
 [self clearCapturedVideoFrame];if(_videoOutput&&[_observedItem.outputs containsObject:_videoOutput])[_observedItem removeOutput:_videoOutput];_videoOutput=nil;if(_observing){[_observedItem removeObserver:self forKeyPath:@"status"];[_observedItem removeObserver:self forKeyPath:@"seekableTimeRanges"];_observing=NO;_observedItem=nil;}}
- (void)dealloc{[_player removeObserver:self forKeyPath:@"externalPlaybackActive" context:PorticoExternalPlaybackContext];[_audioRender invalidate];[_subtitleSelection invalidate];[_subtitleOverlay invalidate];[_audioSelection invalidate];[self removeObservation];[[NSNotificationCenter defaultCenter] removeObserver:self];if(_subtitleTimeObserver)[_player removeTimeObserver:_subtitleTimeObserver];if(_timeObserver)[_player removeTimeObserver:_timeObserver];[_player pause];}
@end
