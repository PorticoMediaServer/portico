#import "PorticoListeningOwner.h"
#import "PorticoPlaybackController.h"
#import "PorticoAudioRender.h"
#import "PorticoQueuePlaybackStore.h"
#import "PorticoListeningPolicy.h"
#import <AVFoundation/AVFoundation.h>
#import <Security/Security.h>
#import <MediaPlayer/MediaPlayer.h>
#import <UIKit/UIKit.h>
#import <math.h>
#import <ImageIO/ImageIO.h>
#import <mach/mach.h>
#import <mach/mach_time.h>

static BOOL Text(id v) { return [v isKindOfClass:NSString.class] && [v length]>0; }
static BOOL WireID(id v) { return Text(v)&&[v length]<=128&&[v rangeOfString:@"^[A-Za-z0-9_-]+$" options:NSRegularExpressionSearch].location!=NSNotFound; }
static BOOL ListeningNumber(id value,double min,double max,BOOL integer) {return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)!=CFBooleanGetTypeID()&&isfinite([value doubleValue])&&[value doubleValue]>=min&&[value doubleValue]<=max&&(!integer||floor([value doubleValue])==[value doubleValue]);}
static BOOL ListeningBool(id value) {return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)==CFBooleanGetTypeID();}
static double Mono(void) { return NSProcessInfo.processInfo.systemUptime; }
static double PauseClock(void) {
 static mach_timebase_info_data_t scale;static dispatch_once_t once;
 dispatch_once(&once,^{mach_timebase_info(&scale);});
 return (double)mach_continuous_time()*scale.numer/scale.denom/1e9;
}
static double Wall(void) { return NSDate.date.timeIntervalSince1970; }
static NSString *NewID(void) { return NSUUID.UUID.UUIDString; }
static NSError *ListeningError(NSString *message) { return [NSError errorWithDomain:@"PorticoListeningOwner" code:1 userInfo:@{NSLocalizedDescriptionKey:message}]; }
static double Timestamp(id value) {
 if(!Text(value))return NAN;
 NSISO8601DateFormatter *f=[NSISO8601DateFormatter new];f.formatOptions=NSISO8601DateFormatWithInternetDateTime|NSISO8601DateFormatWithFractionalSeconds;
 NSDate *d=[f dateFromString:value];if(!d){f.formatOptions=NSISO8601DateFormatWithInternetDateTime;d=[f dateFromString:value];}return d?d.timeIntervalSince1970:NAN;
}

/** Credentialed HTTP has no redirects/cookies/cache, a bounded body and an exact
 * replayable JSON representation. NSURLSession continues independently of JS. */
@interface PorticoListeningRequest : NSObject<NSURLSessionDataDelegate,NSURLSessionTaskDelegate>
@property(nonatomic,strong) NSURLSession *session;
@property(nonatomic,strong) NSMutableData *data;
@property(nonatomic,copy) void (^completion)(NSInteger,id,double);
@property(nonatomic,assign) double started;
@property(nonatomic,assign) BOOL finished;
@property(nonatomic,assign) BOOL binary;
@property(nonatomic,assign) NSUInteger byteLimit;
/** Seconds before the request is given up (0: the default 15). */
@property(nonatomic,assign) double timeout;
/** Extra request headers (v1: If-Match, Idempotency-Key, Content-Type), and the response's. */
@property(nonatomic,copy,nullable) NSDictionary<NSString *,NSString *> *headers;
@property(nonatomic,copy,nullable) NSDictionary *responseHeaders;
- (void)start:(NSURL *)url token:(NSString *)token body:(nullable NSDictionary *)body;
- (void)start:(NSURL *)url token:(NSString *)token body:(nullable NSDictionary *)body method:(NSString *)method;
- (void)cancel;
@end
@implementation PorticoListeningRequest
- (void)start:(NSURL *)url token:(NSString *)token body:(NSDictionary *)body {
 [self start:url token:token body:body method:body?@"POST":@"GET"];
}
- (void)start:(NSURL *)url token:(NSString *)token body:(NSDictionary *)body method:(NSString *)method {
 self.started=Mono();self.data=[NSMutableData new];
 NSURLSessionConfiguration *configuration=NSURLSessionConfiguration.ephemeralSessionConfiguration;
 double timeout=self.timeout>0?self.timeout:15;
 configuration.HTTPCookieStorage=nil;configuration.URLCache=nil;configuration.URLCredentialStorage=nil;configuration.timeoutIntervalForRequest=timeout;configuration.timeoutIntervalForResource=self.timeout>0?timeout:20;
 self.session=[NSURLSession sessionWithConfiguration:configuration delegate:self delegateQueue:NSOperationQueue.mainQueue];
 NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:url cachePolicy:NSURLRequestReloadIgnoringLocalCacheData timeoutInterval:timeout];
 [request setValue:[@"Bearer " stringByAppendingString:token] forHTTPHeaderField:@"Authorization"];
 // This owner reports with observations; the server refuses playback to one that doesn't.
 [request setValue:@"2" forHTTPHeaderField:@"Portico-Playback"];
 request.HTTPMethod=method;
 if(body){request.HTTPBody=[NSJSONSerialization dataWithJSONObject:body options:NSJSONWritingSortedKeys error:nil];[request setValue:@"application/json" forHTTPHeaderField:@"Content-Type"];}
 for(NSString *name in self.headers)[request setValue:self.headers[name] forHTTPHeaderField:name];
 [[self.session dataTaskWithRequest:request] resume];
}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task willPerformHTTPRedirection:(NSHTTPURLResponse *)response newRequest:(NSURLRequest *)request completionHandler:(void (^)(NSURLRequest * _Nullable))handler { handler(nil); }
- (void)URLSession:(NSURLSession *)session dataTask:(NSURLSessionDataTask *)task didReceiveData:(NSData *)data {if(self.data.length+data.length>(self.byteLimit?:1048576)){[task cancel];return;}[self.data appendData:data];}
- (void)URLSession:(NSURLSession *)session task:(NSURLSessionTask *)task didCompleteWithError:(NSError *)error {
 if(self.finished)return;self.finished=YES;
 NSInteger status=error?0:[(NSHTTPURLResponse *)task.response statusCode];
 if(!error&&[task.response isKindOfClass:NSHTTPURLResponse.class])self.responseHeaders=[(NSHTTPURLResponse *)task.response allHeaderFields];
 id value=!error?(self.binary?self.data:[NSJSONSerialization JSONObjectWithData:self.data options:0 error:nil]):nil;
 void (^complete)(NSInteger,id,double)=self.completion;self.completion=nil;[session finishTasksAndInvalidate];self.session=nil;
 if(complete)complete(status,value,self.started);
}
- (void)cancel {self.finished=YES;self.completion=nil;[self.session invalidateAndCancel];self.session=nil;}
@end


#pragma mark - Playback v1 (plan §9.3 C3)

static BOOL V1ID(id v) { return Text(v)&&[v length]<=128&&[v rangeOfString:@"^[A-Za-z0-9_.-]+$" options:NSRegularExpressionSearch].location!=NSNotFound; }
static BOOL V1Revision(id v) { return Text(v)&&[v length]<=256; }
static NSDictionary *V1Dictionary(id v) { return [v isKindOfClass:NSDictionary.class]?v:nil; }
static NSString *V1ErrorCode(id value) { NSDictionary *e=V1Dictionary(V1Dictionary(value)[@"error"]);return Text(e[@"code"])?e[@"code"]:@""; }
static double V1RetryAfter(PorticoListeningRequest *request,id value) {
 NSString *header=nil;for(NSString *key in request.responseHeaders)if([key caseInsensitiveCompare:@"Retry-After"]==NSOrderedSame)header=request.responseHeaders[key];
 double seconds=header?header.doubleValue:[V1Dictionary(V1Dictionary(value)[@"error"])[@"retryAfterSeconds"] doubleValue];
 return isfinite(seconds)&&seconds>0?MIN(30,seconds):2;
}
/** A v1 session as the owner needs it (types.ts parseSession's strict fields). */
static BOOL V1Session(id value) {
 NSDictionary *s=V1Dictionary(value),*p=V1Dictionary(s[@"presentation"]);
 if(!s||!p||!V1ID(s[@"id"])||!V1Revision(s[@"revision"])||![s[@"kind"] isEqual:@"audio"]||!V1ID(s[@"itemId"]))return NO;
 if(!ListeningNumber(p[@"generation"],0,9007199254740991.0,YES)||![p[@"url"] isKindOfClass:NSString.class]||[p[@"url"] length]>4096)return NO;
 NSString *url=p[@"url"];if(url.length&&!([url hasPrefix:@"/v1/"]||[url hasPrefix:@"https://"]||[url hasPrefix:@"http://"]))return NO;
 if(s[@"queue"]&&(!V1Dictionary(s[@"queue"])||!V1ID(s[@"queue"][@"queueId"])||!V1ID(s[@"queue"][@"entryId"])))return NO;
 return YES;
}
static BOOL V1Queue(id value) { NSDictionary *q=V1Dictionary(value);return q&&V1ID(q[@"id"])&&V1Revision(q[@"revision"]); }

/** A v1 presentation's version 2 plan: `audioRender` on the wire, `audio` once client-core parsed it. */
static NSDictionary *V1DecodePlan(NSDictionary *presentation){
 NSDictionary *wire=V1Dictionary(presentation[@"audioRender"]);if([wire[@"version"] isEqual:@2])return wire;
 NSDictionary *parsed=V1Dictionary(presentation[@"audio"]);return [parsed[@"version"] isEqual:@2]?parsed:nil;
}
@implementation PorticoListeningDecodeRender
- (NSDictionary *)plan:(NSDictionary *)presentation origin:(NSString *)origin {
 NSDictionary *plan=V1DecodePlan(presentation);
 if(![PorticoAudioRender playable:plan])return nil;
 NSMutableDictionary *absolute=[plan mutableCopy];if([plan[@"url"] hasPrefix:@"/"])absolute[@"url"]=[origin stringByAppendingString:plan[@"url"]];
 return absolute;
}
- (BOOL)renders:(NSDictionary *)presentation { return [PorticoAudioRender playable:V1DecodePlan(presentation)]; }
- (NSString *)notice:(NSDictionary *)presentation {
 if([self renders:presentation])return nil;
 return @"audio_processing_unavailable";
}
- (BOOL)accepts:(NSDictionary *)presentation origin:(NSString *)origin {
 NSDictionary *plan=[self plan:presentation origin:origin];
 return plan&&[PorticoAudioRender validate:@{@"plan":plan,@"settings":@{@"gapless":@YES,@"crossfadeSeconds":@0,@"normalization":@"off"},@"epoch":@0,@"startSeconds":@0} source:origin binding:@{}]==nil;
}
- (NSDictionary *)command:(NSDictionary *)presentation origin:(NSString *)origin settings:(NSDictionary *)settings epoch:(NSInteger)epoch startSeconds:(double)start prepared:(NSDictionary *)prepared committed:(NSDictionary *)committed {
 NSDictionary *plan=[self plan:presentation origin:origin];if(!plan)return nil;
 NSMutableDictionary *render=[@{@"plan":plan,@"settings":settings,@"epoch":@(epoch),@"startSeconds":@(MAX(0,start)),@"opaqueBinding":@YES} mutableCopy];
 NSDictionary *next=[self plan:prepared[@"presentation"]?:@{} origin:origin];
 if(next&&Text(prepared[@"token"]))render[@"prepared"]=@{@"token":prepared[@"token"],@"plan":next,@"sameAlbum":@([prepared[@"sameAlbum"] boolValue])};
 NSDictionary *successor=[self plan:committed[@"presentation"]?:@{} origin:origin];
 if(successor&&Text(committed[@"token"]))render[@"committed"]=@{@"token":committed[@"token"],@"plan":successor,@"sameAlbum":@([committed[@"sameAlbum"] boolValue])};
 return render;
}
- (double)durationSeconds:(NSDictionary *)presentation {
 NSDictionary *plan=V1DecodePlan(presentation);double frames=[plan[@"durationFrames"] doubleValue],rate=[plan[@"sampleRate"] doubleValue];
 return isfinite(frames)&&isfinite(rate)&&rate>0&&frames>0?frames/rate:0;
}
@end
@implementation PorticoListeningSessionOwner {
 PorticoPlaybackController *_controller;
 id<PorticoListeningRenderBinding> _render;
 double _skipBackSeconds,_skipForwardSeconds;
 NSString *_owner,*_origin,*_token,*_journalKey,*_error,*_notice,*_audioAttempt;
 // The session reported for now; while a committed edge drains, `_audible` is the one heard.
 NSDictionary *_session,*_audible,*_end,*_queueHeader;
 NSMutableDictionary *_queue;
 double _duration,_audibleDuration,_pausedAt;
 BOOL _transitions,_wasPlaying;
 NSString *_intent;double _rate;NSNumber *_preferredRate;
 long long _seq,_audibleSeq;
 // Observations: the epoch this owner reports under (claimed, or handed over by JS), its own
 // clocks since the claim; the seek serial is _seekRevision. The latest goes once a second.
 NSString *_epoch,*_replaces;long long _observed;double _claimedAt,_playClock,_playingSince;
 double _mutationIssuedBaseMs,_mutationClockMono;
 double _leaseUntil,_nextReport,_reportEvery,_retryAt,_backoff,_audioCooldown;
 BOOL _reportNow,_patchWanted,_ended,_endedReported,_terminal,_safetyPaused,_engineFailed,_interrupted,_busy,_invalidated,_emitting,_routeReady,_resetEngine,_rendering;
 NSInteger _routeRevision,_seekRevision,_sequence,_ackSequence,_transitionSequence,_failures,_commitTries,_interruptionRevision,_actionRevision,_intentRevision;
 NSDictionary *_nativeSeek,*_lastFact,*_transition,*_mutationScope,*_stopDisposition;
 // §18: the prepared next entry {token, expiresAtMs, entryId, itemId, presentation}; the committed answer.
 NSDictionary *_prepared,*_committed;BOOL _audioReady,_commitWanted,_fastRetry;
 // A commit whose answer never came may have landed: its token is kept and replayed (idempotent)
 // before anything else moves the queue or ends playback (INT T2).
 NSDictionary *_lostCommit,*_parkedCommit;
 // The prepared token whose album is still being read (the commit waits for it, briefly).
 NSString *_albumFor;
 // A start the owner must make itself (an advance that answered without a session), retried by
 // the loop until the server answers (INT T14).
 NSDictionary *_startPending;
 BOOL _checkEnd;NSInteger _renderFailures,_replayTries;NSString *_noRender;
 NSMutableArray<NSDictionary *> *_actions;
 PorticoListeningRequest *_request,*_metadataRequest,*_artworkRequest;
 NSDictionary *_policy,*_metadata,*_timerCustody;NSNumber *_playbackRun;NSMutableDictionary *_deadlines,*_deadlineMono;
 BOOL _timerStopped;NSInteger _expiredTimerRevision;double _activityWall;NSString *_policyRateKey;
 NSString *_artworkPath;MPMediaItemArtwork *_artwork;
 NSMutableArray<NSDictionary *> *_remoteTargets;
 NSTimer *_timer;UIBackgroundTaskIdentifier _controlTask;
}
- (instancetype)initWithController:(PorticoPlaybackController *)controller render:(id<PorticoListeningRenderBinding>)render {
 if((self=[super init])){
  _controller=controller;_render=render;_controlTask=UIBackgroundTaskInvalid;_remoteTargets=[NSMutableArray new];
  NSNotificationCenter *center=NSNotificationCenter.defaultCenter;
  [center addObserver:self selector:@selector(interruption:) name:AVAudioSessionInterruptionNotification object:nil];
  [center addObserver:self selector:@selector(routeChanged:) name:AVAudioSessionRouteChangeNotification object:nil];
  [center addObserver:self selector:@selector(mediaReset:) name:AVAudioSessionMediaServicesWereResetNotification object:nil];
  [center addObserver:self selector:@selector(foreground:) name:UIApplicationDidBecomeActiveNotification object:nil];
  [center addObserver:self selector:@selector(background:) name:UIApplicationDidEnterBackgroundNotification object:nil];
 }
 return self;
}
- (NSString *)activeOwner { return _owner; }

#pragma mark Adoption

- (BOOL)adopt:(NSDictionary *)context error:(NSError **)error {
 if(_invalidated||!V1Dictionary(context)){if(error)*error=ListeningError(@"listening_owner_invalid");return NO;}
 NSString *origin=context[@"origin"],*token=context[@"accessToken"],*owner=context[@"owner"],*principal=context[@"principalKey"];
 NSURLComponents *url=Text(origin)?[NSURLComponents componentsWithString:origin]:nil;
 BOOL local=[@[@"localhost",@"127.0.0.1",@"[::1]",@"::1"] containsObject:url.host];
 NSDictionary *transport=V1Dictionary(context[@"transport"]);
 BOOL verifiedTransport=transport&&ListeningBool(transport[@"ready"])&&ListeningNumber(transport[@"revision"],0,9007199254740991.0,YES);
 if(!url.host.length||url.user||url.password||url.query||url.fragment||url.path.length||(![url.scheme isEqual:@"https"]&&!([url.scheme isEqual:@"http"]&&(local||verifiedTransport)))||!WireID(owner)||!Text(token)||[token rangeOfCharacterFromSet:NSCharacterSet.newlineCharacterSet].location!=NSNotFound||!Text(principal)||[principal length]>2048){if(error)*error=ListeningError(@"listening_authority_invalid");return NO;}
 NSDictionary *session=context[@"session"];
 if(!V1Session(session)){if(error)*error=ListeningError(@"listening_session_invalid");return NO;}
 id queueId=context[@"queueId"],queueRevision=context[@"queueRevision"];
 if(queueId!=NSNull.null&&queueId&&(!V1ID(queueId)||!V1Revision(queueRevision))){if(error)*error=ListeningError(@"listening_queue_invalid");return NO;}
 if(!ListeningNumber(context[@"lastSeq"],0,9007199254740991.0,YES)||!ListeningNumber(context[@"leaseMs"],0,120000,NO)||!ListeningNumber(context[@"durationSeconds"],0,1e9,NO)||![@[@"playing",@"paused"] containsObject:context[@"intent"]]||!ListeningBool(context[@"transitions"])){if(error)*error=ListeningError(@"listening_handover_invalid");return NO;}
 if(_owner)[self retire:_owner stop:NO];
 _mutationIssuedBaseMs=Wall()*1000+[context[@"mutationClockOffsetMs"] doubleValue];_mutationClockMono=Mono();
 _mutationScope=V1Dictionary(context[@"mutationScope"]);_stopDisposition=nil;
 _owner=[owner copy];_origin=[origin copy];_token=[token copy];_journalKey=[@"listening-v1:" stringByAppendingString:principal];
 _pausedAt=0;_wasPlaying=NO;_session=[session copy];_audible=nil;_end=nil;_queueHeader=nil;
 _queue=V1ID(queueId)?[@{@"id":queueId,@"revision":queueRevision} mutableCopy]:nil;
 _duration=[context[@"durationSeconds"] doubleValue];if(_duration<=0)_duration=[_render durationSeconds:session[@"presentation"]];
 _transitions=[context[@"transitions"] boolValue];_intent=context[@"intent"];_rate=1;_preferredRate=nil;
 _seq=[context[@"lastSeq"] longLongValue];_audibleSeq=0;
 _epoch=nil;_replaces=WireID(context[@"observerEpoch"])?[context[@"observerEpoch"] copy]:nil;_observed=0;_playClock=0;_playingSince=-1;
 _leaseUntil=Mono()+[context[@"leaseMs"] doubleValue]/1000.0;_nextReport=0;_reportEvery=10;_retryAt=0;_backoff=0;_audioCooldown=0;
 _reportNow=YES;_patchWanted=NO;_ended=NO;_endedReported=NO;_terminal=NO;_safetyPaused=NO;_engineFailed=NO;_interrupted=NO;_busy=NO;_resetEngine=NO;_rendering=NO;
 _routeReady=YES;_routeRevision=-1;_seekRevision=0;_sequence=0;_ackSequence=0;_transitionSequence=0;_failures=0;_commitTries=0;_actionRevision=0;_intentRevision=0;
 _nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(MAX(0,[session[@"presentation"][@"startPositionMs"] doubleValue]/1000.0))};
 _lastFact=nil;_transition=nil;_prepared=nil;_committed=nil;_audioReady=NO;_commitWanted=NO;_audioAttempt=nil;_error=nil;_notice=nil;
 _playbackRun=ListeningNumber(context[@"playbackRun"],0,9007199254740991.0,YES)?context[@"playbackRun"]:nil;
 _actions=[NSMutableArray new];_policy=nil;_timerCustody=nil;_metadata=nil;_timerStopped=NO;_expiredTimerRevision=-1;_activityWall=Wall()*1000;_policyRateKey=nil;
 _deadlines=[@{@"sleepAtMs":NSNull.null,@"passoutAtMs":NSNull.null} mutableCopy];_deadlineMono=[NSMutableDictionary new];
 // A commit whose answer was lost (the app was killed) is replayed: it's idempotent by token.
 id saved=[PorticoQueuePlaybackStore read:_journalKey error:nil];
 NSDictionary *journal=V1Dictionary(saved);
 if([journal[@"sessionId"] isEqual:session[@"id"]]&&V1Dictionary(journal[@"commit"])&&[journal[@"commit"][@"queueId"] isEqual:_queue[@"id"]]&&V1ID(journal[@"commit"][@"token"])){
  _prepared=@{@"token":journal[@"commit"][@"token"],@"expiresAtMs":@(Wall()*1000+30000),@"entryId":@"",@"itemId":@"",@"presentation":@{},@"recovered":@YES};_commitWanted=YES;_safetyPaused=YES;
 }
 if(V1Dictionary(journal[@"deadlines"]))for(NSString *field in @[@"sleepAtMs",@"passoutAtMs"]){id at=journal[@"deadlines"][field];if([at isKindOfClass:NSNumber.class]&&isfinite([at doubleValue])&&[at doubleValue]>Wall()*1000){_deadlines[field]=at;_deadlineMono[field]=@(Mono()+MAX(0,[at doubleValue]/1000.0-Wall()));}}
 if(context[@"policy"]&&context[@"policy"]!=NSNull.null&&![self policy:context[@"policy"] owner:_owner]){if(error)*error=ListeningError(@"listening_policy_invalid");[self retire:_owner stop:NO];return NO;}
 if(![self persist]){if(error)*error=ListeningError(@"listening_storage_unavailable");[self retire:_owner stop:NO];return NO;}
 if (@available(iOS 15.0, tvOS 15.0, *)) { [[AVAudioSession sharedInstance] setSupportsMultichannelContent:YES error:nil]; }
 NSError *audioError=nil;[[AVAudioSession sharedInstance] setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeDefault routeSharingPolicy:AVAudioSessionRouteSharingPolicyLongFormAudio options:0 error:&audioError];if(audioError){audioError=nil;[[AVAudioSession sharedInstance] setCategory:AVAudioSessionCategoryPlayback mode:AVAudioSessionModeDefault options:0 error:&audioError];}
 if(audioError){_error=@"audio_session_unavailable";_safetyPaused=YES;}
 if(transport&&![self route:transport owner:_owner]){if(error)*error=ListeningError(@"listening_transport_invalid");[self retire:_owner stop:NO];return NO;}
 [self installRemoteCommands];[self apply];[self emit];[self loadCurrentMetadata];
 __weak PorticoListeningSessionOwner *weakSelf=self;
 _timer=[NSTimer timerWithTimeInterval:0.5 repeats:YES block:^(NSTimer *timer){[weakSelf tick];}];[NSRunLoop.mainRunLoop addTimer:_timer forMode:NSRunLoopCommonModes];[self tick];return YES;
}
/** Only what a relaunch or a lost answer needs: the session, the seq reached, a commit in flight. */
- (BOOL)persist {
 if(!_owner||!_journalKey)return NO;
 NSDictionary *commit=_commitWanted&&_prepared?@{@"token":_prepared[@"token"],@"queueId":_queue[@"id"]?:@""}:_lostCommit;
 NSDictionary *row=@{@"version":@1,@"sessionId":_session[@"id"],@"queueId":_queue[@"id"]?:NSNull.null,@"seq":@(_seq),@"commit":commit?:NSNull.null,@"deadlines":_deadlines?:@{},@"timerStopped":@(_timerStopped),@"expiredTimerRevision":@(_expiredTimerRevision)};
 NSError *error=nil;BOOL ok=[PorticoQueuePlaybackStore write:_journalKey value:row error:&error];
 if(!ok){_safetyPaused=YES;_error=@"playback_storage_unavailable";[self apply];}
 return ok;
}

#pragma mark Engine

- (NSDictionary *)heard { return _audible?:_session; }
- (NSDictionary *)effects { return _policy[@"effects"]?:@{@"gapless":@NO,@"crossfadeSeconds":@0,@"normalization":@"off"}; }
- (BOOL)effectsWanted { NSDictionary *e=[self effects];return [e[@"gapless"] boolValue]||[e[@"crossfadeSeconds"] doubleValue]>0||![e[@"normalization"] isEqual:@"off"]; }
- (BOOL)edgesWanted { NSDictionary *e=[self effects];return [e[@"gapless"] boolValue]||[e[@"crossfadeSeconds"] doubleValue]>0; }
- (NSDictionary *)renderCommand {
 NSDictionary *heard=[self heard],*presentation=heard[@"presentation"];
 // Rendering that keeps failing for this session gives way to the plain stream (INT T8).
 if([_noRender isEqual:heard[@"id"]])return nil;
 // A part-way start in a direct MP3 or ADTS file with the server's seek index is decoded here
 // too, effects or not: the decoder lands on the exact frame, AVPlayer only near it.
 NSDictionary *decoded=V1DecodePlan(presentation);
 BOOL exact=[decoded[@"mode"] isEqual:@"direct"]&&([decoded[@"container"] isEqual:@"mp3"]||[decoded[@"container"] isEqual:@"adts"])&&V1Dictionary(decoded[@"seekIndex"])&&[presentation[@"startPositionMs"] doubleValue]>0;
 if(![_render renders:presentation]||(![self effectsWanted]&&!_rendering&&!exact))return nil;
 _rendering=YES;
 NSDictionary *prepared=_prepared&&!_committed&&![_prepared[@"recovered"] boolValue]?@{@"token":_prepared[@"token"],@"presentation":_prepared[@"presentation"],@"sameAlbum":_prepared[@"sameAlbum"]?:@NO}:nil;
 NSDictionary *committed=_committed?@{@"token":_committed[@"token"],@"presentation":_committed[@"session"][@"presentation"],@"sameAlbum":_committed[@"sameAlbum"]?:@NO}:nil;
 return [_render command:presentation origin:_origin settings:[self effects] epoch:_seekRevision startSeconds:[presentation[@"startPositionMs"] doubleValue]/1000.0 prepared:prepared committed:committed];
}
/** The controller's engine state (the /v2 shape PorticoPlaybackController reads). */
- (NSDictionary *)engineState {
 NSDictionary *heard=[self heard],*presentation=heard[@"presentation"];NSString *url=presentation[@"url"];
 BOOL absolute=[url hasPrefix:@"http"];
 int numerator=(int)llround(_rate*100),denominator=100,a=numerator,b=denominator;while(b){int rest=a%b;a=b;b=rest;}
 NSDictionary *media=url.length?@{@"id":heard[@"id"],@"generation":presentation[@"generation"],@"streamUrl":url,@"absolute":@(absolute),@"mode":[presentation[@"mode"] isEqual:@"direct"]?@"direct":@"hls",@"duration":@(_audible?_audibleDuration:_duration),@"resumeSeconds":@([presentation[@"startPositionMs"] doubleValue]/1000.0)}:(id)NSNull.null;
 return @{@"playbackId":heard[@"id"],@"media":media,@"desired":@{@"state":_intent,@"rate":@{@"numerator":[NSString stringWithFormat:@"%d",numerator/a],@"denominator":[NSString stringWithFormat:@"%d",denominator/a]},@"context":@{@"itemId":heard[@"itemId"]}}};
}
- (BOOL)allowed {
 if(!_routeReady||(_commitWanted&&[_prepared[@"recovered"] boolValue]))return NO;
 return PorticoListeningMayPlay(_owner!=nil,_terminal,_interrupted,_safetyPaused,_ended,[[self heard][@"presentation"][@"url"] length]>0||[self renderCommand]!=nil,Mono(),_leaseUntil,INFINITY);
}
- (void)apply {
 if(!_owner)return;
 if([self allowed]&&[_intent isEqual:@"playing"]){NSError *error=nil;[[AVAudioSession sharedInstance] setActive:YES error:&error];if(error){_safetyPaused=YES;_error=@"audio_session_resume_failed";}}
 BOOL playing=[self allowed]&&[_intent isEqual:@"playing"];
 if(_wasPlaying&&!playing&&_pausedAt==0)_pausedAt=PauseClock();
 if(!_wasPlaying&&playing&&_pausedAt>0){
  double elapsed=MAX(0,PauseClock()-_pausedAt);
  if([[self kind] isEqual:@"audiobook_file"]&&elapsed>=300&&!_nativeSeek&&!_audible&&!_error&&![[_controller listeningSnapshot][@"seeking"] boolValue]){
   double position=[[_controller listeningSnapshot][@"positionSeconds"] doubleValue];
   double amount=PorticoAudiobookResumeDisplacement(elapsed,position,[_policy[@"audiobook"][@"resumeRewindMs"] doubleValue]);
   if(amount>0){[self dropPrepared:nil];_nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(position-amount)};}
  }
  _pausedAt=0;
 }
 _wasPlaying=playing;
 NSDictionary *state=[self engineState];
 NSString *origin=[state[@"media"] isKindOfClass:NSDictionary.class]&&[state[@"media"][@"absolute"] boolValue]?@"":_origin;
 [_controller listeningRoute:_owner ready:_routeReady revision:MAX(0,_routeRevision)];
 [_controller listeningOwner:_owner state:state origin:origin allowed:[self allowed] seek:_audible?nil:_nativeSeek render:[self renderCommand]];
}

#pragma mark Requests

/** One request at a time; a lost answer is retried with backoff, never duplicated. */
- (void)send:(NSString *)method path:(NSString *)path body:(id)body headers:(NSDictionary *)headers completion:(void (^)(NSInteger,id,double,PorticoListeningRequest *))completion {
 [self send:method path:path body:body headers:headers timeout:0 completion:completion];
}
- (void)send:(NSString *)method path:(NSString *)path body:(id)body headers:(NSDictionary *)headers timeout:(double)timeout completion:(void (^)(NSInteger,id,double,PorticoListeningRequest *))completion {
 if(!_owner||_terminal||!_routeReady)return;
 NSString *owner=_owner,*requestToken=_token;
 BOOL fast=_fastRetry;_fastRetry=NO;
 _busy=YES;PorticoListeningRequest *request=[PorticoListeningRequest new];request.headers=headers;request.timeout=timeout;_request=request;
 __weak PorticoListeningSessionOwner *weakSelf=self;__weak PorticoListeningRequest *weakRequest=request;
 request.completion=^(NSInteger status,id value,double started){PorticoListeningRequest *request=weakRequest;
  PorticoListeningSessionOwner *self=weakSelf;if(!self||![self->_owner isEqual:owner]||self->_request!=request||self->_terminal)return;
  self->_request=nil;self->_busy=NO;
  if(status==401&&![self->_token isEqual:requestToken]){self->_retryAt=Mono()+0.5;return;}
  if(status==401||(status==403&&[V1ErrorCode(value) isEqual:@"forbidden"])){[self fail:@"playback_authorization_expired" terminal:YES];return;}
  if(status==0||status>=500||status==429){self->_failures++;NSArray *steps=@[@1,@2,@5,@10,@30];self->_backoff=[steps[(NSUInteger)MIN(self->_failures-1,4)] doubleValue];self->_retryAt=Mono()+(status==429?V1RetryAfter(request,value):fast?0.5:self->_backoff);
   if(Mono()<self->_leaseUntil){self->_error=@"playback_reconnecting";}
   [self apply];[self emit];return;}
  self->_failures=0;
  completion(status,value,started,request);
 };
 [request start:[NSURL URLWithString:[_origin stringByAppendingString:path]] token:_token body:[body isKindOfClass:NSDictionary.class]?body:nil method:method];
}
- (NSString *)sessionPath:(NSString *)suffix { return [NSString stringWithFormat:@"/v1/playback/sessions/%@%@",[_session[@"id"] stringByAddingPercentEncodingWithAllowedCharacters:NSCharacterSet.URLPathAllowedCharacterSet],suffix]; }
- (NSString *)queuePath:(NSString *)command { return [NSString stringWithFormat:@"/v1/queues/%@:%@",[_queue[@"id"] stringByAddingPercentEncodingWithAllowedCharacters:NSCharacterSet.URLPathAllowedCharacterSet],command]; }
/** Events and 404/410 read the same session: reconcile controls, or learn its end. */
- (void)learnEnd {
 NSString *session=_session[@"id"];
 [self send:@"GET" path:[self sessionPath:@""] body:nil headers:nil completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(![self->_session[@"id"] isEqual:session])return;
  NSDictionary *answer=V1Dictionary(value);
  if(status==200&&![answer[@"state"] isEqual:@"ended"]){if(![self reconcileSession:answer keepLocalIntent:self->_patchWanted])self->_retryAt=Mono()+1;return;}
  [self serverEnded:answer];
 }];
}
- (void)serverEnded:(NSDictionary *)session {
 NSDictionary *end=V1Dictionary(session[@"end"]);
 // Ended as completed while a commit's answer was lost: that commit landed. Take its session.
 if([end[@"reason"] isEqual:@"completed"]&&_lostCommit){[self replayLostCommit];return;}
 // Our own accepted completion already drives the queue; it must not terminate this owner.
 if([end[@"reason"] isEqual:@"ended"]&&_endedReported)return;
 _end=Text(end[@"reason"])?@{@"reason":end[@"reason"],@"message":Text(end[@"message"])?end[@"message"]:NSNull.null}:@{@"reason":@"stopped",@"message":NSNull.null};
 [self fail:@"playback_session_ended" terminal:YES];
}
- (BOOL)sessionGone:(NSInteger)status value:(id)value {
 return status==404||status==410||[@[@"session_ended",@"session_not_found",@"lease_expired"] containsObject:V1ErrorCode(value)];
}
- (void)fail:(NSString *)message terminal:(BOOL)terminal {
 _error=message;
 if(terminal){_terminal=YES;_safetyPaused=YES;_leaseUntil=0;[_request cancel];_request=nil;_busy=NO;[_metadataRequest cancel];_metadataRequest=nil;[self clearArtwork];_prepared=nil;_committed=nil;_commitWanted=NO;[_actions removeAllObjects];[self removeRemoteCommands];MPNowPlayingInfoCenter.defaultCenter.nowPlayingInfo=nil;}
 [self apply];[self emit];
}

#pragma mark The loop

- (void)tick {
 if(!_owner)return;
 [self checkDeadlines];
 if(!_actions.count&&!_busy&&!_commitWanted&&!_patchWanted)[self endControlGrace];
 // Audible authority ends 120 s after the last accepted report, whatever the network is doing.
 // Like a pause the listener made: Play (or Retry) resumes once the server answers again.
 if(Mono()>=_leaseUntil&&!_terminal&&!_safetyPaused&&[_intent isEqual:@"playing"]){_safetyPaused=YES;[self dropPrepared:nil];_error=@"playback_confirmation_expired";[self apply];[self emit];}
 if(!_routeReady||_terminal||_busy||Mono()<_retryAt)return;
 if(_commitWanted){[self commit];return;}
 if(_lostCommit&&_actions.count){[self replayLostCommit];return;} // before any :advance
 if(_checkEnd){_checkEnd=NO;[self learnEnd];return;}
 if(_startPending){[self startPendingEntry];return;}
 if(_patchWanted){[self patch];return;}
 if(_actions.count){[self runAction];return;}
 if(_prepared&&!_committed&&[_prepared[@"expiresAtMs"] doubleValue]<=Wall()*1000){[self dropPrepared:@"audio_transition_unavailable"];return;}
 if(_reportNow||Mono()>=_nextReport){[self report];return;}
 [self maybePrepare];
}
/** One observation (P2-21): the latest state, once a second. The first claims the session's epoch
 * (naming JS's, when it handed over); a refused or lost sample is replaced by the next one, never
 * resent. The lease is the one the server last wrote, from the ack. */
- (void)report {
 if(!_session)return;
 NSDictionary *engine=[_controller listeningSnapshot],*binding=V1Dictionary(engine[@"binding"]);
 NSString *state;double position;
 if(_audible){state=@"buffering";position=0;} // the committed successor, before its first sample
 else{
  BOOL bound=[binding[@"sessionId"] isEqual:_session[@"id"]]&&[binding[@"generation"] isEqual:_session[@"presentation"][@"generation"]];
  if(_nativeSeek||(bound&&[engine[@"seeking"] boolValue])){_nextReport=Mono()+1;_reportNow=NO;return;}
  position=bound&&[engine[@"observed"] boolValue]?[engine[@"positionSeconds"] doubleValue]:[_session[@"presentation"][@"startPositionMs"] doubleValue]/1000.0;
  state=_ended?@"ended":(![self allowed]||![_intent isEqual:@"playing"])?@"paused":[engine[@"ready"] boolValue]?@"playing":@"buffering";
 }
 if(_endedReported&&[state isEqual:@"ended"]){_reportNow=NO;_nextReport=INFINITY;return;}
 NSString *session=_session[@"id"];double now=Mono();
 if(_playingSince>=0){_playClock+=now-_playingSince;_playingSince=-1;}
 if([state isEqual:@"playing"])_playingSince=now;
 _reportNow=NO;
 NSString *claimed=_epoch;
 if(!claimed){
  NSDictionary *claim=_replaces?@{@"generation":_session[@"presentation"][@"generation"],@"replaces":_replaces}:@{@"generation":_session[@"presentation"][@"generation"]};
  [self send:@"POST" path:[self sessionPath:@"/observer"] body:claim headers:nil completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
   if(![self->_session[@"id"] isEqual:session])return;
   NSString *granted=V1Dictionary(value)[@"epoch"];
   if(status>=200&&status<300&&WireID(granted)){self->_epoch=[granted copy];self->_replaces=nil;self->_observed=0;self->_claimedAt=Mono();self->_playClock=0;if(self->_playingSince>=0)self->_playingSince=self->_claimedAt;self->_reportNow=YES;return;}
   if([self sessionGone:status value:value]){[self learnEnd];return;}
   self->_nextReport=Mono()+1; // another owner still reports (a handover): claim again shortly
  }];
  return;
 }
 double monotonic=MAX(0,now-_claimedAt)*1000,played=MIN(monotonic,(_playClock+(_playingSince>=0?now-_playingSince:0))*1000);
 long long next=++_observed;
 NSDictionary *body=@{@"epoch":claimed,@"seq":@(next),@"generation":_session[@"presentation"][@"generation"],@"state":state,@"positionMs":@((long long)llround(MAX(0,position)*1000)),
  @"rate":@([state isEqual:@"playing"]?_rate:0),@"monotonicMs":@((long long)floor(monotonic)),@"playClockMs":@((long long)floor(played)),@"seekSerial":@(_seekRevision)};
 [self send:@"PUT" path:[self sessionPath:@"/observation"] body:body headers:nil timeout:1.5 completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(![self->_session[@"id"] isEqual:session])return;
  if(status>=200&&status<300){
   self->_seq=MAX(self->_seq,next);
   double until=Timestamp(V1Dictionary(value)[@"leaseUntil"]);if(isfinite(until))self->_leaseUntil=Mono()+(until-Wall());
   if([self->_error isEqual:@"playback_reconnecting"]||[self->_error isEqual:@"playback_confirmation_expired"])self->_error=nil;
   self->_nextReport=Mono()+1;
   if([state isEqual:@"ended"]){self->_endedReported=YES;self->_nextReport=INFINITY;[self completed];}
   [self emit];return;
  }
  if([V1ErrorCode(value) isEqual:@"observation_epoch_changed"]){self->_replaces=self->_epoch;self->_epoch=nil;self->_reportNow=YES;return;}
  if([self sessionGone:status value:value]){[self learnEnd];return;}
  self->_nextReport=Mono()+1;
 }];
}
/** Pause and resume are the session's desired state (PATCH with If-Match); rate stays local. */
- (void)patch {
 NSString *want=_intent,*session=_session[@"id"];NSInteger intentRevision=_intentRevision;
 [self send:@"PATCH" path:[self sessionPath:@""] body:@{@"state":want} headers:@{@"If-Match":_session[@"revision"],@"Content-Type":@"application/merge-patch+json"} completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(![self->_session[@"id"] isEqual:session])return;
  if(status>=200&&status<300&&[self reconcileSession:V1Dictionary(value) keepLocalIntent:self->_intentRevision!=intentRevision])return;
  if(status==412){
   // Adopt the server's current session; apply the intent again only if it still differs.
   NSDictionary *current=V1Dictionary(V1Dictionary(V1Dictionary(value)[@"error"])[@"current"]);
   if(![self reconcileSession:current keepLocalIntent:YES])self->_patchWanted=NO;
   return;
  }
  if([self sessionGone:status value:value]){self->_patchWanted=NO;[self learnEnd];return;}
  self->_patchWanted=NO;self->_error=@"playback_change_rejected";[self emit];
 }];
}
/** Assign before applying a new binding. Reads do not renew the lease or reset report seq. */
- (BOOL)reconcileSession:(NSDictionary *)fresh keepLocalIntent:(BOOL)keepLocalIntent {
 if(!V1Session(fresh)||![fresh[@"id"] isEqual:_session[@"id"]]||![fresh[@"itemId"] isEqual:_session[@"itemId"]])return NO;
 NSDictionary *old=_session[@"presentation"],*presentation=fresh[@"presentation"];
 if([presentation[@"generation"] longLongValue]<[old[@"generation"] longLongValue])return NO;
 if([fresh[@"state"] isEqual:@"ended"]){_patchWanted=NO;[self serverEnded:fresh];return YES;}
 if(![@[@"playing",@"paused"] containsObject:fresh[@"state"]]||!ListeningNumber(old[@"startPositionMs"],0,9007199254740991.0,NO)||!ListeningNumber(presentation[@"startPositionMs"],0,9007199254740991.0,NO))return NO;
 BOOL sourceChanged=![presentation[@"generation"] isEqual:old[@"generation"]]||![presentation[@"url"] isEqual:old[@"url"]];
 BOOL serverSeek=[presentation[@"startPositionMs"] doubleValue]!=[old[@"startPositionMs"] doubleValue];
 NSString *intent=keepLocalIntent?_intent:fresh[@"state"];BOOL intentChanged=![intent isEqual:_intent];
 NSDictionary *clock=[_controller listeningSnapshot],*binding=V1Dictionary(clock[@"binding"]);
 double seconds=[presentation[@"startPositionMs"] doubleValue]/1000.0;
 if(!serverSeek&&sourceChanged){
  if(ListeningNumber(_nativeSeek[@"positionSeconds"],0,1e9,NO))seconds=[_nativeSeek[@"positionSeconds"] doubleValue];
  else if(!_audible&&[clock[@"observed"] boolValue]&&[binding[@"sessionId"] isEqual:_session[@"id"]]&&[binding[@"generation"] isEqual:old[@"generation"]]&&ListeningNumber(clock[@"positionSeconds"],0,1e9,NO))seconds=[clock[@"positionSeconds"] doubleValue];
 }
 _session=[fresh copy];_intent=intent;_patchWanted=keepLocalIntent&&![intent isEqual:fresh[@"state"]];
 if(sourceChanged||serverSeek){
  _nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(seconds)};
  _ended=NO;_endedReported=NO;_audioAttempt=nil;_reportNow=YES;_pausedAt=0;
  if(_audible){
   // Retire the buffered old deck, retaining the successor's lease and report sequence.
   _transition=@{@"previousSessionId":_transition[@"previousSessionId"]?:_audible[@"id"],@"queue":_queueHeader?:NSNull.null};_transitionSequence=_sequence+1;
   _audible=nil;_committed=nil;_prepared=nil;_audioReady=NO;_commitWanted=NO;_rendering=NO;
   _metadata=nil;[_metadataRequest cancel];_metadataRequest=nil;[self clearArtwork];
   [_controller resetListeningEngine:_owner];[self loadCurrentMetadata];
  }
 }
 if(sourceChanged||serverSeek||intentChanged){_reportNow=YES;[self dropPrepared:nil];}
 [self persist];[self apply];[self emit];return YES;
}

#pragma mark Queue moves

- (BOOL)mayAdvance {
 if(_timerStopped)return NO;
 NSDictionary *postPlay=V1Dictionary(_queueHeader[@"postPlay"]);
 if(postPlay[@"autoplay"]&&![postPlay[@"autoplay"] boolValue])return NO;
 return YES;
}
/** The track ended and the server knows: move on if the viewer's policy allows. */
- (void)completed {
 [self checkDeadlines];
 if(!_queue||![self mayAdvance]){_safetyPaused=YES;[self apply];[self emit];return;}
 NSDictionary *next=V1Dictionary(_queueHeader[@"next"]);
 if(next&&![next[@"available"] boolValue]){[self emit];return;} // the end of the queue
 [self enqueue:@"completion" entry:nil];
}
- (void)enqueue:(NSString *)reason entry:(NSString *)entryId {
 if(_actions.count>=16){[_actions removeObjectAtIndex:0];}
 [_actions addObject:@{@"reason":reason,@"entryId":entryId?:NSNull.null,@"key":NewID(),@"revision":@(++_actionRevision),@"tries":@0}];
}
- (BOOL)advance:(NSString *)reason entry:(NSString *)entryId owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal||![@[@"next",@"previous",@"entry"] containsObject:reason]||!_queue)return NO;
 if([reason isEqual:@"entry"]!=V1ID(entryId))return NO;
 [self beginControlGrace];[self resetInactivity];
 // Between commit and boundary the server's queue is already on the successor: Next, Previous
 // and a lock-screen skip mean "that one, now" (INT T3). A chosen entry then moves from there.
 [self finishBoundaryTimer];
 if(_committed){[self promoteCommitted];if(![reason isEqual:@"entry"])return YES;}
 if([reason isEqual:@"previous"]&&!_audible&&[[_controller listeningSnapshot][@"positionSeconds"] doubleValue]>3)return [self action:@"seek" value:@0 owner:owner];
 [self dropPrepared:nil];_safetyPaused=NO;_ended=NO;
 [self enqueue:reason entry:[reason isEqual:@"entry"]?entryId:nil];[self emit];[self tick];return YES;
}
- (void)runAction {
 NSDictionary *action=_actions.firstObject;
 [self dropPrepared:nil]; // any queue command cancels a preparation (§18.2)
 NSMutableDictionary *body=[@{@"reason":action[@"reason"]} mutableCopy];if(V1ID(action[@"entryId"]))body[@"entryId"]=action[@"entryId"];
 NSString *session=_session[@"id"];
 [self send:@"POST" path:[self queuePath:@"advance"] body:body headers:@{@"If-Match":_queue[@"revision"],@"Idempotency-Key":action[@"key"]} completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(self->_actions.firstObject!=action||![self->_session[@"id"] isEqual:session])return;
  if(status==412){
   // The queue changed elsewhere (Up Next on another device): take its revision, ask once more.
   NSDictionary *current=V1Dictionary(V1Dictionary(V1Dictionary(value)[@"error"])[@"current"]);
   // A commit given up while offline did land after all: replay its token before anything else.
   if(self->_parkedCommit){self->_lostCommit=self->_parkedCommit;self->_parkedCommit=nil;self->_replayTries=0;if(V1Queue(current))[self acceptQueue:current];return;}
   [self->_actions removeObjectAtIndex:0];
   if(V1Queue(current)&&[action[@"tries"] integerValue]<1){[self acceptQueue:current];NSMutableDictionary *again=[action mutableCopy];again[@"key"]=NewID();again[@"tries"]=@1;[self->_actions insertObject:again atIndex:0];}
   else{self->_error=@"playback_queue_changed";[self emit];}
   return;
  }
  if(status==503){self->_retryAt=Mono()+V1RetryAfter(request,value);return;} // queue_building: the same request again
  [self->_actions removeObjectAtIndex:0];
  if(status==409&&[V1ErrorCode(value) isEqual:@"queue_ended"]){self->_safetyPaused=YES;[self apply];[self emit];return;}
  NSDictionary *result=V1Dictionary(value),*queue=V1Dictionary(result[@"queue"])?:result;
  if(status<200||status>=300||!V1Queue(queue)){self->_error=[self sessionGone:status value:value]?@"playback_queue_unavailable":@"playback_queue_advance_failed";[self emit];return;}
  [self acceptQueue:queue];
  if(V1Session(result[@"session"])){[self moveTo:result[@"session"] started:started queue:queue automatic:[action[@"reason"] isEqual:@"completion"]];return;}
  NSDictionary *current=V1Dictionary(queue[@"current"]);
  if(V1ID(current[@"entryId"]))[self startEntry:current[@"entryId"] queue:queue automatic:[action[@"reason"] isEqual:@"completion"]];
  else{self->_safetyPaused=YES;[self apply];[self emit];}
 }];
}
/** An advance that didn't start the next session itself (spec §18.5 says it does; older servers
 * don't): the owner starts it, and the loop retries until the server answers (INT T14). */
- (void)startEntry:(NSString *)entryId queue:(NSDictionary *)queue automatic:(BOOL)automatic {
 _startPending=@{@"automatic":@(automatic),@"key":NewID(),@"previous":_session[@"id"],@"queue":queue?:NSNull.null,
  @"body":@{@"queue":@{@"queueId":_queue[@"id"],@"entryId":entryId},@"state":_intent,@"startFrom":@"resume",@"replacesSessionId":_session[@"id"]}};
 [self tick];
}
- (void)startPendingEntry {
 NSDictionary *pending=_startPending;
 [self send:@"POST" path:@"/v1/playback/sessions" body:pending[@"body"] headers:@{@"Idempotency-Key":pending[@"key"]} completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(self->_startPending!=pending)return;
  if(status==202){double wait=[V1Dictionary(value)[@"retryAfterMs"] doubleValue]/1000.0;self->_retryAt=Mono()+MIN(10,MAX(.25,isfinite(wait)&&wait>0?wait:1));return;} // preparing: the same request again
  self->_startPending=nil;
  if((status==200||status==201)&&V1Session(value)){[self moveTo:value started:started queue:V1Dictionary(pending[@"queue"]) automatic:[pending[@"automatic"] boolValue]];return;}
  [self fail:@"playback_next_failed" terminal:NO];
 }];
}
- (void)acceptQueue:(NSDictionary *)queue {
 if(![queue[@"id"] isEqual:_queue[@"id"]])return;
 _queue[@"revision"]=queue[@"revision"];_queueHeader=[queue copy];
}
/** The owner moved to another session (an advance, a start): report it from now on. */
- (void)moveTo:(NSDictionary *)session started:(double)started queue:(NSDictionary *)queue {
 [self moveTo:session started:started queue:queue automatic:NO];
}
- (void)moveTo:(NSDictionary *)session started:(double)started queue:(NSDictionary *)queue automatic:(BOOL)automatic {
 [self carryTimerTo:session automatic:automatic];
 NSString *previous=_audible[@"id"]?:_session[@"id"];
 if(previous||_transition){_transition=@{@"previousSessionId":_transition?_transition[@"previousSessionId"]:previous,@"queue":queue?:NSNull.null};_transitionSequence=_sequence+1;}
 _pausedAt=0;_wasPlaying=NO;_session=[session copy];_audible=nil;_seq=0;_epoch=nil;_replaces=nil;_leaseUntil=started+120;_nextReport=0;_reportNow=YES;_ended=NO;_endedReported=NO;_error=nil;_notice=nil;
 _prepared=nil;_committed=nil;_commitWanted=NO;_audioReady=NO;_renderFailures=0;_startPending=nil;_parkedCommit=nil;_replayTries=0;
 _pausedAt=0;_wasPlaying=NO;_intent=(_timerStopped||[session[@"state"] isEqual:@"paused"])?@"paused":@"playing";_safetyPaused=NO;_engineFailed=NO;_audioAttempt=nil;_rendering=NO;_policyRateKey=nil;
 _duration=[_render durationSeconds:session[@"presentation"]];
 _nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(MAX(0,[session[@"presentation"][@"startPositionMs"] doubleValue]/1000.0))};
 _lastFact=nil;_metadata=nil;[_metadataRequest cancel];_metadataRequest=nil;[self clearArtwork];
 [self applyPreferredRate];[self persist];[self apply];[self emit];[self loadCurrentMetadata];
}

#pragma mark Gapless and crossfade (§18.2, §18.3)

- (void)maybePrepare {
 NSDictionary *engine=[_controller listeningSnapshot];
 if(!_transitions||!_queue||_prepared||_audible||_committed||!_rendering||![self edgesWanted]||![_intent isEqual:@"playing"]||![self allowed]||![self mayAdvance]||![engine[@"ready"] boolValue]||![engine[@"observed"] boolValue]||[engine[@"seeking"] boolValue]||_nativeSeek||[_audioAttempt isEqual:_session[@"id"]]||Mono()<_audioCooldown)return;
 if(![_render renders:_session[@"presentation"]])return;
 NSDictionary *next=V1Dictionary(_queueHeader[@"next"]);if(next&&![next[@"available"] boolValue])return;
 double remaining=_duration-[engine[@"positionSeconds"] doubleValue];
 if(_duration<=0||remaining<=0||remaining>MIN(40,30*_rate)||![self timerAllowsEdge:remaining/MAX(.25,_rate)])return;
 _audioAttempt=_session[@"id"]; // one try per track
 NSString *session=_session[@"id"];id timerPolicy=_policy[@"revision"];
 [self send:@"POST" path:[self queuePath:@"prepare-next"] body:@{@"sessionId":session,@"sessionGeneration":_session[@"presentation"][@"generation"]} headers:@{@"If-Match":_queue[@"revision"],@"Idempotency-Key":NewID()} completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(![self->_session[@"id"] isEqual:session]||self->_actions.count||!self->_audioAttempt||(timerPolicy!=self->_policy[@"revision"]&&![timerPolicy isEqual:self->_policy[@"revision"]]))return;
  NSDictionary *answer=V1Dictionary(value),*presentation=V1Dictionary(answer[@"presentation"]);
  double expires=Timestamp(answer[@"expiresAt"]);
  if(status!=200||!V1ID(answer[@"token"])||!V1ID(answer[@"entryId"])||!V1ID(answer[@"itemId"])||!isfinite(expires)||expires<=Wall()||!presentation||![self->_render accepts:presentation origin:self->_origin]){
   if(status==412){NSDictionary *current=V1Dictionary(V1Dictionary(V1Dictionary(value)[@"error"])[@"current"]);if(V1Queue(current))[self acceptQueue:current];}
   return; // no edge for this track: it ends and advances as usual
  }
  if(![self->_intent isEqual:@"playing"]||![self mayAdvance])return;
  self->_prepared=@{@"token":answer[@"token"],@"expiresAtMs":@(expires*1000),@"entryId":answer[@"entryId"],@"itemId":answer[@"itemId"],@"presentation":presentation};
  self->_audioReady=NO;[self apply];[self emit];[self learnAlbum:answer[@"itemId"] token:answer[@"token"]];
 }];
}
/** A local cancellation (pause, seek, rate, any queue command): the candidate deck goes, no commit. */
/** Consecutive tracks of one album join gaplessly, never crossfaded (plan §9.6 rule 4): the next
 * track on the disc, or the first of the next disc; with track numbers unknown, being on the same
 * album is enough. The same rule as client-core `consecutiveOnAlbum`, which the web engine uses. */
static BOOL ListeningConsecutiveOnAlbum(NSString *album,NSInteger disc,NSInteger track,NSString *nextAlbum,NSInteger nextDisc,NSInteger nextTrack){
 if(!Text(album)||![album isEqual:nextAlbum])return NO;
 if(track<=0||nextTrack<=0)return YES;
 NSInteger d=disc>0?disc:1,nd=nextDisc>0?nextDisc:1;
 return (nd==d&&nextTrack==track+1)||(nd==d+1&&nextTrack==1);
}
- (void)learnAlbum:(NSString *)itemId token:(NSString *)token {
 NSString *album=_metadata[@"albumId"];if(!Text(album)||!V1ID(itemId))return;
 NSInteger disc=[_metadata[@"discNumber"] integerValue],track=[_metadata[@"trackNumber"] integerValue];NSString *owner=_owner;
 _albumFor=token;
 PorticoListeningRequest *request=[PorticoListeningRequest new];__weak PorticoListeningSessionOwner *weakSelf=self;
 request.completion=^(NSInteger status,id value,double started){PorticoListeningSessionOwner *self=weakSelf;
  if(!self)return;
  if([self->_albumFor isEqual:token])self->_albumFor=nil;
  if(![self->_owner isEqual:owner]||![self->_prepared[@"token"] isEqual:token])return;
  NSDictionary *song=V1Dictionary(V1Dictionary(value)[@"song"]);
  if(status==200&&ListeningConsecutiveOnAlbum(album,disc,track,song[@"albumId"],[song[@"discNumber"] integerValue],[song[@"trackNumber"] integerValue])){NSMutableDictionary *prepared=[self->_prepared mutableCopy];prepared[@"sameAlbum"]=@YES;self->_prepared=prepared;[self apply];}
  if(self->_commitWanted)[self tick]; // a commit waiting for this answer goes now
 };
 [request start:[NSURL URLWithString:[_origin stringByAppendingFormat:@"/v1/items/%@",itemId]] token:_token body:nil];
}
- (void)dropPrepared:(NSString *)message {
 if(!_prepared||_committed)return;
 _prepared=nil;_audioReady=NO;_commitWanted=NO;_audioCooldown=Mono()+3;
 if(message)_notice=message;
 [self persist];[self apply];
}
- (void)commit {
 if(!_prepared){_commitWanted=NO;return;}
 NSDictionary *prepared=_prepared;NSString *session=_session[@"id"];BOOL recovered=[prepared[@"recovered"] boolValue];
 // A commit that can't be confirmed before the current track runs out is given up at once: the
 // track then ends and advances as usual, rather than leaving silence while the network recovers.
 double remaining=_duration-[[_controller listeningSnapshot][@"positionSeconds"] doubleValue];
 if(!recovered&&![self timerAllowsEdge:MAX(0,remaining)/MAX(.25,_rate)]){_commitWanted=NO;[self dropPrepared:nil];return;}
 if(!recovered&&([prepared[@"expiresAtMs"] doubleValue]<=Wall()*1000||++_commitTries>4||(_commitTries>1&&remaining<1.0))){
  // No answer came: the commit may have landed. Keep its token; the track ends as usual, and
  // the token is replayed before any :advance (or when the old session reads as completed).
  if(_commitTries>1)_lostCommit=@{@"token":prepared[@"token"],@"queueId":_queue[@"id"]?:@""};
  _commitWanted=NO;[self dropPrepared:@"audio_transition_unavailable"];return;}
 // Whether the edge is gapless (same album) or a crossfade is decided when it's committed, so
 // the commit waits for the album answer while there's time; after that it crossfades.
 if(!recovered&&[_albumFor isEqual:prepared[@"token"]]&&remaining>1.5){_commitTries--;return;}
 if(recovered&&++_commitTries>4){_commitWanted=NO;_prepared=nil;_safetyPaused=NO;[self persist];[self apply];[self emit];return;}
 if(![self persist])return; // the token is saved before it is sent
 _fastRetry=YES; // a lost answer is asked again quickly: the commit is idempotent by token
 [self send:@"POST" path:[self queuePath:@"commit-next"] body:@{@"token":prepared[@"token"]} headers:nil completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(self->_prepared!=prepared||![self->_session[@"id"] isEqual:session])return;
  NSDictionary *answer=V1Dictionary(value),*queue=V1Dictionary(answer[@"queue"]),*next=answer[@"session"];
  if(status==200&&V1Queue(queue)&&V1Session(next)&&(recovered||[self->_render accepts:next[@"presentation"] origin:self->_origin])){
   self->_commitWanted=NO;[self acceptQueue:queue];
   if(recovered){self->_prepared=nil;self->_safetyPaused=YES;[self moveTo:next started:started queue:queue];return;}
   // The server moved the queue: the new session reports from now on, the old deck drains to
   // the boundary. The old grant fetches nothing more.
   self->_committed=@{@"token":prepared[@"token"],@"session":next,@"sameAlbum":prepared[@"sameAlbum"]?:@NO};
   self->_audible=self->_session;self->_audibleSeq=self->_seq;self->_audibleDuration=self->_duration;
   self->_session=[next copy];self->_seq=0;self->_epoch=nil;self->_replaces=nil;self->_leaseUntil=started+120;self->_reportNow=YES;self->_endedReported=NO;
   self->_duration=[self->_render durationSeconds:next[@"presentation"]];
   [self persist];[self apply];[self emit];return;
  }
  // 410 prepared_expired, 409 prepared_canceled, 404: finish this track, then advance as usual.
  self->_commitWanted=NO;self->_lostCommit=nil;[self dropPrepared:@"audio_transition_unavailable"];
  if(recovered){self->_safetyPaused=NO;[self apply];}
 }];
}
/** Replays a commit whose answer was lost (idempotent by token): a session means it landed, and
 * that session plays from its start; a refusal means it didn't, and the queue moves as usual. */
- (void)replayLostCommit {
 NSDictionary *lost=_lostCommit;NSString *session=_session[@"id"];
 // While the network is down, a replay can't answer either: after six quick tries the queue moves
 // as usual. If the commit had landed, that :advance meets a 412 whose current entry has moved
 // on, and the token is replayed once more then (runAction).
 if(++_replayTries>6){_lostCommit=nil;_replayTries=0;_parkedCommit=lost;[self persist];[self tick];return;}
 _fastRetry=YES;
 [self send:@"POST" path:[self queuePath:@"commit-next"] body:@{@"token":lost[@"token"]} headers:nil completion:^(NSInteger status,id value,double started,PorticoListeningRequest *request){
  if(self->_lostCommit!=lost||![self->_session[@"id"] isEqual:session])return;
  self->_lostCommit=nil;self->_replayTries=0;self->_parkedCommit=nil;
  NSDictionary *answer=V1Dictionary(value),*queue=V1Dictionary(answer[@"queue"]),*next=answer[@"session"];
  if(status==200&&V1Queue(queue)&&V1Session(next)){[self->_actions removeAllObjects];[self acceptQueue:queue];self->_safetyPaused=NO;[self moveTo:next started:started queue:queue];return;}
  [self persist];[self tick]; // it didn't land: the pending :advance (or the end) proceeds
 }];
}
/** The committed successor plays now, from its start, as an ordinary start (INT T3): a skip in the
 * drain, or the successor failing or ending before its boundary. */
- (void)promoteCommitted {
 NSDictionary *next=_committed[@"session"];if(!V1Session(next))return;
 [_controller resetListeningEngine:_owner];
 [self moveTo:next started:Mono() queue:_queueHeader automatic:YES];
}
- (void)boundary:(NSDictionary *)fact {
 if(!_committed||![_committed[@"token"] isEqual:fact[@"token"]])return;
 [self carryTimerTo:_session automatic:YES];
 NSString *previous=_audible[@"id"]?:_session[@"id"];
 if(previous||_transition){_transition=@{@"previousSessionId":_transition?_transition[@"previousSessionId"]:previous,@"queue":_queueHeader?:NSNull.null};_transitionSequence=_sequence+1;}
 _audible=nil;_committed=nil;_prepared=nil;_audioReady=NO;_audioAttempt=nil;_lastFact=fact;_nativeSeek=nil;_ended=NO;_engineFailed=NO;_reportNow=YES;_policyRateKey=nil;_renderFailures=0;
 _metadata=nil;[_metadataRequest cancel];_metadataRequest=nil;[self clearArtwork];
 // Rebind without a seek: the engine already made the buffered deck the primary.
 [self applyPreferredRate];[self persist];[self apply];[self emit];[self loadCurrentMetadata];
}

#pragma mark Engine facts

- (void)fact:(NSDictionary *)fact {
 NSDictionary *heard=[self heard],*binding=V1Dictionary(fact[@"binding"]);
 if(!_owner||_terminal||![binding[@"sessionId"] isEqual:heard[@"id"]]||![binding[@"generation"] isEqual:heard[@"presentation"][@"generation"]])return;
 NSString *type=fact[@"type"];_lastFact=fact;
 if([type isEqual:@"ready"]&&[[_controller listeningSnapshot][@"ready"] boolValue])_engineFailed=NO;
 if([type isEqual:@"audioPrepared"]&&[_prepared[@"token"] isEqual:fact[@"token"]])_audioReady=YES;
 if([type isEqual:@"audioCommit"]&&_audioReady&&!_committed&&[_prepared[@"token"] isEqual:fact[@"token"]]){
  if([self mayAdvance]&&[_intent isEqual:@"playing"]&&[self allowed]){_commitWanted=YES;_commitTries=0;[self beginControlGrace];[self tick];}
  else [self dropPrepared:nil];
 }
 if([type isEqual:@"audioBoundary"]){[self boundary:fact];return;}
 // The successor failed, or the old deck ended with no boundary: promote it with an ordinary start.
 if(_committed&&[@[@"ended",@"audioPreparationFailed",@"audioRenderFailed",@"error"] containsObject:type]){if([type isEqual:@"ended"])_ended=YES;[self checkDeadlines];[self promoteCommitted];return;}
 if([type isEqual:@"audioPreparationFailed"])[self dropPrepared:@"audio_transition_unavailable"];
 if([type isEqual:@"seekApplied"]&&[fact[@"seekRevision"] isEqual:_nativeSeek[@"revision"]]){_nativeSeek=nil;_reportNow=YES;[self apply];}
 if([type isEqual:@"seekFailed"]){_nativeSeek=nil;_safetyPaused=YES;_error=@"playback_seek_unavailable";[self dropPrepared:nil];[self apply];}
 if([type isEqual:@"ended"]&&!_ended&&!_committed){_ended=YES;_reportNow=YES;[self checkDeadlines];}
 if([type isEqual:@"audioRenderFailed"]&&++_renderFailures>=1&&![_noRender isEqual:heard[@"id"]]){
  // Rendering keeps failing: play this session's ordinary stream from here, and say so quietly.
  double position=[[_controller listeningSnapshot][@"positionSeconds"] doubleValue];
  _noRender=heard[@"id"];_rendering=NO;[self dropPrepared:nil];[_controller resetListeningEngine:_owner];
  _nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(isfinite(position)?MAX(0,position):0)};
  _notice=@"audio_gapless_unavailable";_reportNow=YES;[self apply];[self emit];return;
 }
 if([type isEqual:@"error"]||[type isEqual:@"audioRenderFailed"]){_engineFailed=YES;_safetyPaused=YES;_error=@"audio_render_failed";_reportNow=YES;[self dropPrepared:nil];[self apply];}
 if([type isEqual:@"progress"]){
  [self checkDeadlines];
  // While paused nothing moves: no snapshot per engine tick (INT T13).
  if(!([_intent isEqual:@"playing"]&&[self allowed]))return;
 }
 [self emit];
}

#pragma mark Intents

/** A JS command is admitted on this queue against the original engine and queue occurrence. */
- (BOOL)actionFor:(NSDictionary *)expected action:(NSString *)action value:(NSNumber *)value owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal||((_committed||_audible)&&![action isEqual:@"pause"])||![expected isKindOfClass:NSDictionary.class])return NO;
 NSDictionary *heard=[self heard],*actual=[_controller listeningSnapshot];
 NSDictionary *wanted=V1Dictionary(expected[@"binding"]),*binding=V1Dictionary(actual[@"binding"]);
 if(!V1ID(wanted[@"lease"])||!ListeningNumber(wanted[@"intentId"],1,9007199254740991.0,YES)||!V1ID(wanted[@"sessionId"])||
    !ListeningNumber(wanted[@"generation"],0,9007199254740991.0,YES)||!V1ID(wanted[@"itemId"])||!ListeningNumber(expected[@"commandRevision"],0,9007199254740991.0,YES))return NO;
 for(NSString *key in @[@"lease",@"intentId",@"sessionId",@"generation",@"itemId"])if(![wanted[key] isEqual:binding[key]])return NO;
 if(![wanted[@"sessionId"] isEqual:heard[@"id"]]||![wanted[@"generation"] isEqual:heard[@"presentation"][@"generation"]]||
    ![wanted[@"itemId"] isEqual:heard[@"itemId"]]||![expected[@"commandRevision"] isEqual:actual[@"commandRevision"]])return NO;
 NSDictionary *queue=V1Dictionary(heard[@"queue"]),*target=V1Dictionary(expected[@"queue"]);
 if(queue){if(!V1ID(target[@"queueId"])||!V1ID(target[@"entryId"])||![target[@"queueId"] isEqual:queue[@"queueId"]]||![target[@"entryId"] isEqual:queue[@"entryId"]])return NO;}
 else if(expected[@"queue"]!=NSNull.null)return NO;
 return [self action:action value:value owner:owner];
}
- (BOOL)action:(NSString *)action value:(NSNumber *)value owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal||![@[@"pause",@"resume",@"seek",@"rate",@"retry",@"next",@"previous"] containsObject:action])return NO;
 if([@[@"seek",@"rate"] containsObject:action]&&!ListeningNumber(value,0,1e9,NO))return NO;
 if([action isEqual:@"rate"]&&!PorticoListeningRateAllowed([self kind],value))return NO;
 if([action isEqual:@"retry"]&&value&&(id)value!=NSNull.null&&!ListeningNumber(value,0,1e9,NO))return NO;
 if([@[@"next",@"previous"] containsObject:action])return [self advance:action entry:nil owner:owner];
 [self checkDeadlines];[self beginControlGrace];[self resetInactivity];
 if([action isEqual:@"resume"]&&(_ended||_engineFailed)){action=@"retry";value=_ended?@0:nil;}
 if([@[@"pause",@"resume",@"retry"] containsObject:action])_intentRevision++;
 if([action isEqual:@"pause"]){if([_intent isEqual:@"playing"])_pausedAt=PauseClock();[self dropPrepared:nil];_intent=@"paused";_patchWanted=YES;_reportNow=YES;[self apply];}
 if([action isEqual:@"resume"]){_intent=@"playing";_safetyPaused=NO;_timerStopped=NO;_error=nil;_patchWanted=YES;_reportNow=YES;[self apply];}
 if([action isEqual:@"seek"])_pausedAt=0;
 if([action isEqual:@"seek"]&&_committed){[self promoteCommitted];[self persist];[self emit];return !_terminal;}
 if([action isEqual:@"seek"]&&!_audible){
  double seconds=MAX(0,_duration>0?MIN(value.doubleValue,_duration):value.doubleValue);
  [self dropPrepared:nil];_audioAttempt=nil;_ended=NO;_endedReported=NO;_nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(seconds)};[self apply];
 }
 if([action isEqual:@"rate"]){_preferredRate=value;_rate=value.doubleValue;if(!_committed)[self dropPrepared:nil];_reportNow=YES;[self apply];}
 if([action isEqual:@"retry"]){
  double seconds=[value isKindOfClass:NSNumber.class]?value.doubleValue:[[_controller listeningSnapshot][@"positionSeconds"] doubleValue];
  [self dropPrepared:nil];_intent=@"playing";_safetyPaused=NO;_engineFailed=NO;_timerStopped=NO;_ended=NO;_endedReported=NO;_error=nil;_resetEngine=YES;_patchWanted=YES;_reportNow=YES;_renderFailures=0;
  [_controller resetListeningEngine:_owner];_resetEngine=NO;
  _nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(MAX(0,seconds))};[self apply];
 }
 [self persist];[self emit];[self tick];return !_terminal;
}
- (BOOL)sessionUpdated:(NSString *)sessionId owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal)return NO;
 if(([sessionId isEqual:_session[@"id"]]&&!_endedReported)||[sessionId isEqual:_audible[@"id"]]){_checkEnd=YES;[self tick];}
 return YES;
}
- (BOOL)credentials:(NSString *)token owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal||!Text(token)||[token rangeOfCharacterFromSet:NSCharacterSet.newlineCharacterSet].location!=NSNotFound)return NO;
 _token=[token copy];return YES;
}
- (BOOL)route:(NSDictionary *)route owner:(NSString *)owner {
 if(![_owner isEqual:owner]||!V1Dictionary(route)||!ListeningBool(route[@"ready"])||!ListeningNumber(route[@"revision"],0,9007199254740991.0,YES))return NO;
 NSInteger revision=[route[@"revision"] longLongValue];BOOL ready=[route[@"ready"] boolValue];
 if(revision<_routeRevision)return YES;
 NSString *origin=ready?route[@"origin"]:_origin;
 NSURLComponents *url=Text(origin)?[NSURLComponents componentsWithString:origin]:nil;
 if(ready&&(!url.host.length||url.user||url.password||url.query||url.fragment||url.path.length||![@[@"http",@"https"] containsObject:url.scheme]))return NO;
 if(revision==_routeRevision)return ready==_routeReady&&(!ready||[_origin isEqual:origin]);
 BOOL changed=ready&&![_origin isEqual:origin];
 NSDictionary *clock=[_controller listeningSnapshot];double position=[clock[@"positionSeconds"] doubleValue];
 _routeRevision=revision;_routeReady=ready;
 if(!ready||changed){
  [_request cancel];_request=nil;_busy=NO;[_metadataRequest cancel];_metadataRequest=nil;[self clearArtwork];[self dropPrepared:nil];
  if([clock[@"observed"] boolValue]&&isfinite(position)&&position>=0&&!_audible){_nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":@(position)};}
  _reportNow=YES;_retryAt=0;
 }
 if(ready)_origin=[origin copy];
 [self apply];[self emit];return YES;
}

#pragma mark Policy and timers (as the /v2 owner)

- (BOOL)validPolicy:(id)value {
 if(![value isKindOfClass:NSDictionary.class])return NO;
 for(NSString *key in @[@"revision",@"activityRevision",@"timerRevision"])if(!ListeningNumber(value[key],0,9007199254740991.0,YES))return NO;
 if(!ListeningNumber(value[@"activityAtMs"],1,9007199254740991.0,YES))return NO;
 NSDictionary *effects=value[@"effects"];
 if(!PorticoListeningEffectsValid(effects))return NO;
 id preferences=value[@"preferences"];
 if(!PorticoListeningPreferencesValid(preferences))return NO;
 if(!PorticoAudiobookPreferencesValid(value[@"audiobook"]))return NO;
 id timer=value[@"timer"];
 if(timer!=NSNull.null){
  if(![timer isKindOfClass:NSDictionary.class]||!Text(timer[@"label"])||[timer[@"label"] length]>512||!WireID(timer[@"libraryId"]))return NO;
  id choice=timer[@"choice"];
  if([choice isKindOfClass:NSNumber.class]){if(![@[@15,@30,@45,@60,@90] containsObject:choice]||!ListeningNumber(timer[@"wallDeadline"],1,9007199254740991.0,YES))return NO;}
  else if([@[@"chapter",@"item",@"book"] containsObject:choice]){
   if(!V1ID(timer[@"itemId"])||![self validTimerOccurrence:timer[@"occurrence"]])return NO;
   if([choice isEqual:@"book"]){
    NSDictionary *book=V1Dictionary(timer[@"bookBoundary"]),*revision=V1Dictionary(book[@"revision"]);NSArray *parts=book[@"items"];
    if(!ListeningNumber(revision[@"catalog"],0,9007199254740991.0,YES)||!ListeningNumber(revision[@"viewer"],0,9007199254740991.0,YES)||![parts isKindOfClass:NSArray.class]||parts.count<1||parts.count>1000||![parts.lastObject isEqual:timer[@"itemId"]]||[NSSet setWithArray:parts].count!=parts.count)return NO;
    for(id item in parts)if(!V1ID(item))return NO;
   }
   if([choice isEqual:@"chapter"]&&(!V1ID(timer[@"sessionId"])||!ListeningNumber(timer[@"generation"],0,9007199254740991.0,YES)||!ListeningNumber(timer[@"endSeconds"],0,1e9,NO)))return NO;
   if([choice isEqual:@"book"]&&!V1ID(timer[@"bookId"]))return NO;
  }else return NO;
 }
 return YES;
}
- (void)installDeadline:(NSString *)key value:(id)value {
 value=value?:NSNull.null;if([value isEqual:_deadlines[key]])return;
 _deadlines[key]=value;
 if(value==NSNull.null)[_deadlineMono removeObjectForKey:key];else _deadlineMono[key]=@(Mono()+MAX(0,[value doubleValue]/1000.0-Wall()));
}
- (void)resetInactivity {
 _activityWall=Wall()*1000;
 NSInteger minutes=[_policy[@"preferences"] isKindOfClass:NSDictionary.class]?[_policy[@"preferences"][@"passoutMinutes"] integerValue]:0;
 [self installDeadline:@"passoutAtMs" value:minutes?@((long long)(_activityWall+minutes*60000)):NSNull.null];
}
- (void)applyPreferredRate {
 if(!_owner||_terminal)return;
 NSDictionary *book=_policy[@"audiobook"];
 if([[self kind] isEqual:@"audiobook_file"])[self skipBack:book?([book[@"skipBackMs"] doubleValue]/1000):15 forward:book?([book[@"skipForwardMs"] doubleValue]/1000):30];
 else [self skipBack:30 forward:30];
 NSNumber *preferred=PorticoPreferredListeningRate([self kind],_policy[@"preferences"]);if(!preferred)return;
 double rate=preferred.doubleValue;
 NSString *key=[NSString stringWithFormat:@"%@:%g",_session[@"id"],rate];
 if([_policyRateKey isEqual:key])return;_policyRateKey=key;
 if(fabs(_rate-rate)>.00001){_rate=rate;_preferredRate=@(rate);_reportNow=YES;}
}
/** Music or a book: the item's kind from its metadata, else music. */
- (NSString *)kind { return [_metadata[@"kind"] isEqual:@"audiobook_file"]?@"audiobook_file":@"song"; }
- (BOOL)validTimerOccurrence:(id)value {
 NSDictionary *o=V1Dictionary(value);if(!o||!V1ID(o[@"sessionId"])||!V1ID(o[@"itemId"])||!ListeningNumber(o[@"generation"],0,9007199254740991.0,YES)||!ListeningNumber(o[@"playbackRun"],0,9007199254740991.0,YES)||o[@"owner"]&&!V1ID(o[@"owner"]))return NO;
 NSDictionary *q=V1Dictionary(o[@"queue"]);return o[@"queue"]==NSNull.null||(q&&V1ID(q[@"queueId"])&&V1ID(q[@"entryId"]));
}
- (BOOL)matchesTimer:(NSDictionary *)o {
 NSDictionary *heard=[self heard],*actual=V1Dictionary(heard[@"queue"]),*target=V1Dictionary(o[@"queue"]);
 if(!o||!_playbackRun||![o[@"playbackRun"] isEqual:_playbackRun]||![o[@"sessionId"] isEqual:heard[@"id"]]||![o[@"generation"] isEqual:heard[@"presentation"][@"generation"]]||![o[@"itemId"] isEqual:heard[@"itemId"]]||(o[@"owner"]&&![o[@"owner"] isEqual:_owner]))return NO;
 return actual?(target&&[target[@"queueId"] isEqual:actual[@"queueId"]]&&[target[@"entryId"] isEqual:actual[@"entryId"]]):o[@"queue"]==NSNull.null;
}
- (NSDictionary *)activeTimer {
 if([_policy[@"timerRevision"] longLongValue]==_expiredTimerRevision)return nil;
 NSDictionary *timer=V1Dictionary(_policy[@"timer"]);
 if([@[@"chapter",@"item",@"book"] containsObject:timer[@"choice"]]&&![self matchesTimer:_timerCustody]){[self finishPolicyTimer];return nil;}
 return timer;
}
- (void)finishBoundaryTimer {if([@[@"chapter",@"item",@"book"] containsObject:[self activeTimer][@"choice"]])[self finishPolicyTimer];}
- (void)carryTimerTo:(NSDictionary *)next automatic:(BOOL)automatic {
 NSDictionary *timer=[self activeTimer];if(![@[@"chapter",@"item",@"book"] containsObject:timer[@"choice"]])return;
 NSDictionary *heard=[self heard],*before=V1Dictionary(heard[@"queue"]),*after=V1Dictionary(next[@"queue"]);NSArray *parts=V1Dictionary(timer[@"bookBoundary"])[@"items"];NSUInteger index=[parts indexOfObject:heard[@"itemId"]];
 if([timer[@"choice"] isEqual:@"book"]&&automatic&&[self matchesTimer:_timerCustody]&&before&&after&&[before[@"queueId"] isEqual:after[@"queueId"]]&&![before[@"entryId"] isEqual:after[@"entryId"]]&&index!=NSNotFound&&index+1<parts.count&&[parts[index+1] isEqual:next[@"itemId"]]){
  NSMutableDictionary *custody=[_timerCustody mutableCopy];custody[@"sessionId"]=next[@"id"];custody[@"generation"]=next[@"presentation"][@"generation"];custody[@"itemId"]=next[@"itemId"];custody[@"queue"]=[after copy];_timerCustody=[custody copy];
 }else [self finishPolicyTimer];
}
- (BOOL)policy:(NSDictionary *)value owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal||![self validPolicy:value])return NO;
 if(_policy&&[value[@"revision"] longLongValue]<[_policy[@"revision"] longLongValue])return YES;
 if(_policy&&[value[@"revision"] isEqual:_policy[@"revision"]])return [_policy isEqual:value];
 NSDictionary *previous=_policy;BOOL newTimer=!previous||![previous[@"timerRevision"] isEqual:value[@"timerRevision"]];NSDictionary *timer=V1Dictionary(value[@"timer"]);
 if(!newTimer&&![previous[@"timer"] isEqual:value[@"timer"]]&&!(value[@"timer"]==NSNull.null&&[value[@"timerRevision"] longLongValue]==_expiredTimerRevision))return NO;
 if(newTimer&&[@[@"chapter",@"item",@"book"] containsObject:timer[@"choice"]]){
  NSDictionary *occurrence=V1Dictionary(timer[@"occurrence"]);
  if(![self matchesTimer:occurrence]||_ended||_actions.count||_startPending||_committed||_audible||_commitWanted||_lostCommit||_parkedCommit)return NO;
  NSDictionary *binding=V1Dictionary([_controller listeningSnapshot][@"binding"]);
  if(![binding[@"sessionId"] isEqual:occurrence[@"sessionId"]]||![binding[@"generation"] isEqual:occurrence[@"generation"]]||![binding[@"itemId"] isEqual:occurrence[@"itemId"]])return NO;
  if([timer[@"choice"] isEqual:@"book"]&&![V1Dictionary(timer[@"bookBoundary"])[@"items"] containsObject:[self heard][@"itemId"]])return NO;
  if(![timer[@"choice"] isEqual:@"book"]&&![timer[@"itemId"] isEqual:occurrence[@"itemId"]])return NO;
  if([timer[@"choice"] isEqual:@"chapter"]&&(![timer[@"sessionId"] isEqual:occurrence[@"sessionId"]]||![timer[@"generation"] isEqual:occurrence[@"generation"]]))return NO;
  if([timer[@"choice"] isEqual:@"book"]&&_metadata[@"bookId"]&&![_metadata[@"bookId"] isEqual:timer[@"bookId"]])return NO;
  _timerCustody=[occurrence copy];
 }else if(newTimer)_timerCustody=nil;
 _policy=[value copy];
 BOOL activity=!previous||![previous[@"activityRevision"] isEqual:value[@"activityRevision"]];
 if(activity)_activityWall=[value[@"activityAtMs"] doubleValue];
 NSInteger oldMinutes=[previous[@"preferences"] isKindOfClass:NSDictionary.class]?[previous[@"preferences"][@"passoutMinutes"] integerValue]:0;
 NSInteger minutes=[value[@"preferences"] isKindOfClass:NSDictionary.class]?[value[@"preferences"][@"passoutMinutes"] integerValue]:0;
 if(activity||minutes!=oldMinutes)[self installDeadline:@"passoutAtMs" value:minutes?@((long long)(_activityWall+minutes*60000)):NSNull.null];
 if((!previous||![previous[@"timerRevision"] isEqual:value[@"timerRevision"]])&&[value[@"timerRevision"] longLongValue]!=_expiredTimerRevision){
  NSDictionary *timer=[value[@"timer"] isKindOfClass:NSDictionary.class]?value[@"timer"]:nil;
  [self installDeadline:@"sleepAtMs" value:timer[@"wallDeadline"]?:NSNull.null];
 }
 if((newTimer&&[@[@"chapter",@"item",@"book"] containsObject:timer[@"choice"]])||(previous&&(![previous[@"effects"] isEqual:value[@"effects"]]||![self mayAdvance])))[self dropPrepared:nil];
 if(![previous[@"effects"] isEqual:value[@"effects"]]&&!_rendering&&[self effectsWanted]&&[_render renders:[self heard][@"presentation"]]&&_lastFact&&!_audible){
  // Rendering starts mid-track: from where the listener is.
  _nativeSeek=@{@"revision":@(++_seekRevision),@"positionSeconds":[_controller listeningSnapshot][@"positionSeconds"]?:@0};
 }
 [self applyPreferredRate];[self checkDeadlines];if(![self persist]){[self apply];[self emit];return NO;}[self apply];[self emit];return !_terminal;
}
- (BOOL)timerAllowsEdge:(double)remaining {
 NSDictionary *timer=[self activeTimer];
 if([timer[@"choice"] isEqual:@"chapter"]||([@[@"item",@"book"] containsObject:timer[@"choice"]]&&[timer[@"itemId"] isEqual:[self heard][@"itemId"]]))return NO;
 for(NSString *field in @[@"sleepAtMs",@"passoutAtMs"]){id at=_deadlines[field];if([at isKindOfClass:NSNumber.class]&&([at doubleValue]<=Wall()*1000+remaining*1000||[_deadlineMono[field] doubleValue]<=Mono()+remaining))return NO;}
 return YES;
}
- (void)finishPolicyTimer {_timerCustody=nil;_expiredTimerRevision=[_policy[@"timerRevision"] longLongValue];[self installDeadline:@"sleepAtMs" value:NSNull.null];}
- (void)expireListening {
 if(_timerStopped)return;
 _timerStopped=YES;[self finishPolicyTimer];[self installDeadline:@"passoutAtMs" value:NSNull.null];
 [self dropPrepared:nil];_intentRevision++;_intent=@"paused";_patchWanted=YES;_reportNow=YES;
 [self beginControlGrace];[self persist];[self apply];[self emit];
}
- (void)checkDeadlines {
 if(!_owner||_timerStopped)return;
 BOOL expired=NO;
 for(NSString *key in @[@"sleepAtMs",@"passoutAtMs"]){id value=_deadlines[key];if([value isKindOfClass:NSNumber.class]&&PorticoListeningTimerExpired(Wall()*1000,Mono(),[value doubleValue],[_deadlineMono[key] doubleValue]))expired=YES;}
 NSDictionary *timer=[self activeTimer],*heard=[self heard],*engine=[_controller listeningSnapshot];
 if([timer[@"choice"] isEqual:@"chapter"]){
  if(![timer[@"sessionId"] isEqual:heard[@"id"]]||![timer[@"generation"] isEqual:heard[@"presentation"][@"generation"]])[self finishPolicyTimer];
  else if([V1Dictionary(engine[@"binding"])[@"sessionId"] isEqual:heard[@"id"]]&&[V1Dictionary(engine[@"binding"])[@"generation"] isEqual:heard[@"presentation"][@"generation"]]&&![engine[@"seeking"] boolValue]&&[engine[@"observed"] boolValue]&&[engine[@"positionSeconds"] doubleValue]>=[timer[@"endSeconds"] doubleValue])expired=YES;
 }
 if(_ended&&[@[@"item",@"book"] containsObject:timer[@"choice"]]&&[timer[@"itemId"] isEqual:heard[@"itemId"]])expired=YES;
 if(expired)[self expireListening];
}
- (BOOL)deadlines:(NSDictionary *)deadlines owner:(NSString *)owner {
 if(![_owner isEqual:owner]||_terminal||![deadlines isKindOfClass:NSDictionary.class])return NO;
 for(NSString *key in @[@"sleepAtMs",@"passoutAtMs"]){id value=deadlines[key];if(value!=NSNull.null&&!ListeningNumber(value,1,9007199254740991.0,YES))return NO;}
 for(NSString *key in @[@"sleepAtMs",@"passoutAtMs"])[self installDeadline:key value:deadlines[key]];
 if(![self persist])return NO;[self checkDeadlines];[self emit];return YES;
}

#pragma mark Snapshot

- (NSDictionary *)snapshot:(NSString *)owner {
 if(![_owner isEqual:owner])return @{};
 [self checkDeadlines];return [self makeSnapshot];
}
- (NSDictionary *)makeSnapshot {
 NSMutableDictionary *snapshot=[[_controller listeningSnapshot] mutableCopy];
 NSDictionary *heard=[self heard],*binding=V1Dictionary(snapshot[@"binding"]);
 BOOL currentBinding=[binding[@"sessionId"] isEqual:heard[@"id"]]&&[binding[@"generation"] isEqual:heard[@"presentation"][@"generation"]];
 if(!currentBinding){snapshot[@"binding"]=NSNull.null;snapshot[@"ready"]=@NO;snapshot[@"observed"]=@NO;}
 else snapshot[@"binding"]=@{@"lease":binding[@"lease"],@"intentId":binding[@"intentId"],@"sessionId":heard[@"id"],@"generation":heard[@"presentation"][@"generation"],@"itemId":heard[@"itemId"]};
 snapshot[@"owner"]=_owner;snapshot[@"sequence"]=@(_sequence);
 snapshot[@"session"]=heard;snapshot[@"durationSeconds"]=@(_audible?_audibleDuration:_duration);snapshot[@"lastSeq"]=@(_audible?_audibleSeq:_seq);
 snapshot[@"queue"]=_queue?[_queue copy]:NSNull.null;
 snapshot[@"intent"]=(_safetyPaused||_interrupted||_ended||(![self allowed]&&Mono()>=_leaseUntil))?@"paused":_intent;
 snapshot[@"eqApplied"]=@(currentBinding&&!_ended&&!_terminal&&[snapshot[@"eqApplied"] boolValue]);
 snapshot[@"levelingApplied"]=@(currentBinding&&!_ended&&!_terminal&&[snapshot[@"levelingApplied"] boolValue]);
 if(![snapshot[@"levelingApplied"] boolValue]||!ListeningNumber(snapshot[@"levelingGainDb"],-60,30,NO))
  [snapshot removeObjectForKey:@"levelingGainDb"];
 snapshot[@"rate"]=@(_rate);snapshot[@"ended"]=@(_ended);snapshot[@"terminal"]=@(_terminal);snapshot[@"error"]=_error?:NSNull.null;
 snapshot[@"deadlines"]=[_deadlines copy];snapshot[@"timerStopped"]=@(_timerStopped);snapshot[@"expiredTimerRevision"]=@(_expiredTimerRevision);snapshot[@"policyRevision"]=_policy[@"revision"]?:@0;
 NSString *notice=_notice?:([self effectsWanted]?[_render notice:heard[@"presentation"]]:nil);if(notice)snapshot[@"noticeCode"]=notice;
 if(_end)snapshot[@"end"]=_end;
 NSMutableArray *routes=[NSMutableArray new];for(AVAudioSessionPortDescription *port in AVAudioSession.sharedInstance.currentRoute.outputs)[routes addObject:port.portName];snapshot[@"route"]=[routes componentsJoinedByString:@", "];
 if(_transition)snapshot[@"transition"]=_transition;
 return snapshot;
}
- (void)emit {
 if(!_owner||_emitting)return;_emitting=YES;++_sequence;[self updateNowPlaying];NSDictionary *snapshot=[self makeSnapshot];if(self.onSnapshot)self.onSnapshot(snapshot);_emitting=NO;
}
- (BOOL)acknowledge:(NSString *)owner sequence:(NSNumber *)sequence {
 if(![_owner isEqual:owner]||![sequence isKindOfClass:NSNumber.class]||sequence.integerValue<1||sequence.integerValue>_sequence)return NO;
 _ackSequence=MAX(_ackSequence,sequence.integerValue);if(_ackSequence>=_transitionSequence)_transition=nil;return YES;
}

#pragma mark Now Playing, remote commands, the audio session (as the /v2 owner)

- (void)metadata:(NSDictionary *)metadata owner:(NSString *)owner {
 NSDictionary *heard=[self heard];
 if(![_owner isEqual:owner]||_terminal||![metadata isKindOfClass:NSDictionary.class]||![metadata[@"playbackId"] isEqual:heard[@"id"]]||![metadata[@"itemId"] isEqual:heard[@"itemId"]])return;
 NSMutableDictionary *clean=[@{@"itemId":metadata[@"itemId"]} mutableCopy];for(NSString *key in @[@"title",@"artist",@"album",@"kind",@"albumId"]){NSString *value=metadata[key];if([value isKindOfClass:NSString.class]&&value.length<=4096)clean[key]=value;}
 for(NSString *key in @[@"discNumber",@"trackNumber"])if(ListeningNumber(metadata[key],0,100000,YES))clean[key]=metadata[key];
 if(!clean[@"albumId"]&&_metadata[@"albumId"]&&[_metadata[@"itemId"] isEqual:metadata[@"itemId"]])for(NSString *key in @[@"albumId",@"discNumber",@"trackNumber"])if(_metadata[key])clean[key]=_metadata[key]; // JS's metadata doesn't carry them
 if(metadata[@"bookId"])clean[@"bookId"]=V1ID(metadata[@"bookId"])?metadata[@"bookId"]:@"";
 else if([_metadata[@"itemId"] isEqual:metadata[@"itemId"]]&&_metadata[@"bookId"])clean[@"bookId"]=_metadata[@"bookId"];
 _metadata=clean;
 NSDictionary *timer=[self activeTimer];if([timer[@"choice"] isEqual:@"book"]&&clean[@"bookId"]&&![clean[@"bookId"] isEqual:timer[@"bookId"]])[self finishPolicyTimer];[self loadArtwork:metadata[@"posterUrl"]];[self applyPreferredRate];[self updateNowPlaying];
}
- (void)updateNowPlaying {
 if(!_owner||_terminal)return;
 NSDictionary *engine=[_controller listeningSnapshot];
 NSMutableDictionary *info=[@{MPMediaItemPropertyTitle:_metadata[@"title"]?:@"",MPMediaItemPropertyArtist:_metadata[@"artist"]?:@"",MPMediaItemPropertyAlbumTitle:_metadata[@"album"]?:@"",MPMediaItemPropertyPlaybackDuration:@(_audible?_audibleDuration:_duration),MPNowPlayingInfoPropertyElapsedPlaybackTime:engine[@"positionSeconds"]?:@0,MPNowPlayingInfoPropertyPlaybackRate:engine[@"effectiveRate"]?:@0,MPNowPlayingInfoPropertyDefaultPlaybackRate:@1,MPNowPlayingInfoPropertyMediaType:@(MPNowPlayingInfoMediaTypeAudio)} mutableCopy];
 if(_artwork)info[MPMediaItemPropertyArtwork]=_artwork;MPNowPlayingInfoCenter.defaultCenter.nowPlayingInfo=info;
}
- (void)loadCurrentMetadata {
 [_metadataRequest cancel];_metadataRequest=nil;
 NSDictionary *heard=[self heard];NSString *owner=_owner,*playback=heard[@"id"],*item=heard[@"itemId"];
 if(!_owner||_terminal||!_routeReady||!V1ID(item))return;
 PorticoListeningRequest *request=[PorticoListeningRequest new];_metadataRequest=request;
 __weak PorticoListeningSessionOwner *weakSelf=self;request.completion=^(NSInteger status,id value,double started){PorticoListeningSessionOwner *self=weakSelf;if(!self||![self->_owner isEqual:owner]||![[self heard][@"id"] isEqual:playback]||self->_metadataRequest!=request||self->_terminal)return;self->_metadataRequest=nil;
  if(status!=200||![value isKindOfClass:NSDictionary.class]||![value[@"id"] isEqual:item])return;
  NSDictionary *song=[value[@"song"] isKindOfClass:NSDictionary.class]?value[@"song"]:@{},*book=[value[@"bookFile"] isKindOfClass:NSDictionary.class]?value[@"bookFile"]:@{};
  double duration=[value[@"durationMs"] doubleValue]/1000.0;if(self->_duration<=0&&isfinite(duration)&&duration>0&&!self->_audible)self->_duration=duration;
  [self metadata:@{@"playbackId":playback,@"itemId":item,@"title":value[@"title"]?:@"",@"artist":song[@"artist"]?:@"",@"album":song[@"albumTitle"]?:book[@"bookTitle"]?:@"",@"bookId":book[@"bookId"]?:@"",@"kind":[value[@"kind"] isKindOfClass:NSString.class]?value[@"kind"]:@"",@"posterUrl":value[@"posterUrl"]?:NSNull.null,@"albumId":Text(song[@"albumId"])?song[@"albumId"]:@"",@"discNumber":[song[@"discNumber"] isKindOfClass:NSNumber.class]?song[@"discNumber"]:@0,@"trackNumber":[song[@"trackNumber"] isKindOfClass:NSNumber.class]?song[@"trackNumber"]:@0} owner:owner];
 };[request start:[NSURL URLWithString:[_origin stringByAppendingFormat:@"/v1/items/%@",item]] token:_token body:nil];
}
- (void)clearArtwork {[_artworkRequest cancel];_artworkRequest=nil;_artworkPath=nil;_artwork=nil;}
- (void)loadArtwork:(id)value {
 if(!_routeReady){[self clearArtwork];return;}
 NSString *item=[self heard][@"itemId"];
 if(!Text(value)||![value isEqual:[NSString stringWithFormat:@"/v1/items/%@/art/poster",item]]||[value length]>8192){[self clearArtwork];return;}
 if([_artworkPath isEqual:value])return;[self clearArtwork];_artworkPath=[value copy];
 NSString *owner=_owner,*path=_artworkPath;
 PorticoListeningRequest *request=[PorticoListeningRequest new];request.binary=YES;request.byteLimit=4*1024*1024;_artworkRequest=request;
 __weak PorticoListeningSessionOwner *weakSelf=self;request.completion=^(NSInteger status,id bytes,double started){PorticoListeningSessionOwner *self=weakSelf;if(!self||![self->_owner isEqual:owner]||self->_artworkRequest!=request||self->_terminal)return;self->_artworkRequest=nil;
  if(status!=200||![bytes isKindOfClass:NSData.class])return;
  CGImageSourceRef source=CGImageSourceCreateWithData((__bridge CFDataRef)bytes,(__bridge CFDictionaryRef)@{(__bridge id)kCGImageSourceShouldCache:@NO});if(!source)return;
  CGImageRef image=CGImageSourceCreateThumbnailAtIndex(source,0,(__bridge CFDictionaryRef)@{(__bridge id)kCGImageSourceCreateThumbnailFromImageAlways:@YES,(__bridge id)kCGImageSourceThumbnailMaxPixelSize:@512,(__bridge id)kCGImageSourceCreateThumbnailWithTransform:@YES});CFRelease(source);if(!image)return;
  UIImage *thumbnail=[UIImage imageWithCGImage:image];CGImageRelease(image);if(![self->_artworkPath isEqual:path])return;
  self->_artwork=[[MPMediaItemArtwork alloc] initWithBoundsSize:thumbnail.size requestHandler:^UIImage *(CGSize size){return thumbnail;}];[self updateNowPlaying];
 };[request start:[NSURL URLWithString:[_origin stringByAppendingString:path]] token:_token body:nil];
}
- (void)addRemote:(MPRemoteCommand *)command handler:(MPRemoteCommandHandlerStatus (^)(MPRemoteCommandEvent *event))handler {
 command.enabled=YES;id target=[command addTargetWithHandler:handler];[_remoteTargets addObject:@{@"command":command,@"target":target}];
}
- (void)skipBack:(double)back forward:(double)forward {
 if(!isfinite(back)||!isfinite(forward))return;
 _skipBackSeconds=MAX(5,MIN(120,back));_skipForwardSeconds=MAX(5,MIN(120,forward));
 MPRemoteCommandCenter *center=MPRemoteCommandCenter.sharedCommandCenter;
 if(_remoteTargets.count){center.skipBackwardCommand.preferredIntervals=@[@(_skipBackSeconds)];center.skipForwardCommand.preferredIntervals=@[@(_skipForwardSeconds)];}
}
/** A lock-screen or headset command, resolved against the owner that installed it. */
- (void)remote:(NSString *)action value:(NSNumber *)value owner:(NSString *)installedOwner {
 if(![_owner isEqual:installedOwner]||_terminal)return;
 if([action isEqual:@"stop"]){[self retire:installedOwner stop:YES];return;}
 NSString *resolved=action;NSNumber *resolvedValue=value;
 if([action isEqual:@"toggle"])resolved=([self allowed]&&[_intent isEqual:@"playing"])?@"pause":@"resume";
 if([action isEqual:@"skip-back"]||[action isEqual:@"skip-forward"]){BOOL back=[action isEqual:@"skip-back"];value=@(back?-(_skipBackSeconds>0?_skipBackSeconds:30):(_skipForwardSeconds>0?_skipForwardSeconds:30));action=@"skip";}
 if([action isEqual:@"skip"]){resolvedValue=@(MAX(0,[[_controller listeningSnapshot][@"positionSeconds"] doubleValue]+value.doubleValue));resolved=@"seek";}
 [self action:resolved value:resolvedValue owner:installedOwner];
}
- (void)installRemoteCommands {
 [self removeRemoteCommands];MPRemoteCommandCenter *center=MPRemoteCommandCenter.sharedCommandCenter;
 __weak PorticoListeningSessionOwner *weakSelf=self;NSString *installedOwner=_owner;
 void (^invoke)(NSString *,NSNumber *)=^(NSString *action,NSNumber *value){
  dispatch_async(dispatch_get_main_queue(),^{[weakSelf remote:action value:value owner:installedOwner];});
 };
 void (^transport)(MPRemoteCommand *,NSString *)=^(MPRemoteCommand *command,NSString *action){[weakSelf addRemote:command handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event){invoke(action,nil);return MPRemoteCommandHandlerStatusSuccess;}];};
 transport(center.playCommand,@"resume");transport(center.pauseCommand,@"pause");transport(center.togglePlayPauseCommand,@"toggle");transport(center.stopCommand,@"stop");
 if(_queue){transport(center.nextTrackCommand,@"next");transport(center.previousTrackCommand,@"previous");}
 [self addRemote:center.changePlaybackPositionCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event){if(![event isKindOfClass:MPChangePlaybackPositionCommandEvent.class])return MPRemoteCommandHandlerStatusCommandFailed;invoke(@"seek",@(((MPChangePlaybackPositionCommandEvent *)event).positionTime));return MPRemoteCommandHandlerStatusSuccess;}];
 for(MPSkipIntervalCommand *command in @[center.skipForwardCommand,center.skipBackwardCommand]){BOOL backward=command==center.skipBackwardCommand;double preferred=backward?(_skipBackSeconds>0?_skipBackSeconds:30):(_skipForwardSeconds>0?_skipForwardSeconds:30);command.preferredIntervals=@[@(preferred)];[self addRemote:command handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event){(void)event;invoke(backward?@"skip-back":@"skip-forward",nil);return MPRemoteCommandHandlerStatusSuccess;}];}
 center.changePlaybackRateCommand.supportedPlaybackRates=@[@0.5,@0.75,@1,@1.25,@1.5,@1.75,@2];
 [self addRemote:center.changePlaybackRateCommand handler:^MPRemoteCommandHandlerStatus(MPRemoteCommandEvent *event){if(![event isKindOfClass:MPChangePlaybackRateCommandEvent.class])return MPRemoteCommandHandlerStatusCommandFailed;invoke(@"rate",@(((MPChangePlaybackRateCommandEvent *)event).playbackRate));return MPRemoteCommandHandlerStatusSuccess;}];
}
- (void)removeRemoteCommands {for(NSDictionary *entry in _remoteTargets){MPRemoteCommand *command=entry[@"command"];[command removeTarget:entry[@"target"]];command.enabled=NO;}[_remoteTargets removeAllObjects];}
- (void)interruption:(NSNotification *)notification {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self interruption:notification];});return;}
 if(!_owner||_terminal)return;
 if([notification.userInfo[AVAudioSessionInterruptionTypeKey] unsignedIntegerValue]==AVAudioSessionInterruptionTypeBegan){_interrupted=YES;_interruptionRevision=_actionRevision;[self dropPrepared:nil];_reportNow=YES;[self apply];[self emit];}
 else{BOOL resume=([notification.userInfo[AVAudioSessionInterruptionOptionKey] unsignedIntegerValue]&AVAudioSessionInterruptionOptionShouldResume)!=0;_interrupted=NO;[self checkDeadlines];if(!resume){_intentRevision++;_intent=@"paused";_patchWanted=YES;}_reportNow=YES;[self apply];[self tick];[self emit];}
}
- (void)routeChanged:(NSNotification *)notification {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self routeChanged:notification];});return;}
 if(!_owner||_terminal)return;
 if([notification.userInfo[AVAudioSessionRouteChangeReasonKey] unsignedIntegerValue]==AVAudioSessionRouteChangeReasonOldDeviceUnavailable)[self action:@"pause" value:nil owner:_owner];
 [self emit];
}
- (void)mediaReset:(NSNotification *)notification {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self mediaReset:notification];});return;}
 if(!_owner||_terminal)return;
 _engineFailed=YES;_safetyPaused=YES;[self dropPrepared:nil];_error=@"audio_service_restarted";_reportNow=YES;[self apply];[self emit];
}
- (void)foreground:(NSNotification *)notification {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self foreground:notification];});return;}if(_owner&&!_terminal){[self checkDeadlines];[self emit];[self tick];}}
- (void)background:(NSNotification *)notification {
 if(!NSThread.isMainThread){dispatch_async(dispatch_get_main_queue(),^{[self background:notification];});return;}if(_owner){[self checkDeadlines];_reportNow=YES;[self persist];[self emit];[self tick];}}
- (void)beginControlGrace {
 if(_controlTask!=UIBackgroundTaskInvalid)return;
 __weak PorticoListeningSessionOwner *weakSelf=self;
 _controlTask=[UIApplication.sharedApplication beginBackgroundTaskWithName:@"Portico listening control" expirationHandler:^{PorticoListeningSessionOwner *self=weakSelf;if(!self)return;UIBackgroundTaskIdentifier task=self->_controlTask;self->_controlTask=UIBackgroundTaskInvalid;if(task!=UIBackgroundTaskInvalid)[UIApplication.sharedApplication endBackgroundTask:task];}];
}
- (void)endControlGrace {if(_controlTask!=UIBackgroundTaskInvalid){UIBackgroundTaskIdentifier task=_controlTask;_controlTask=UIBackgroundTaskInvalid;[UIApplication.sharedApplication endBackgroundTask:task];}}

#pragma mark Retirement

/** Stop shares the coordinator's entry and lease. There is no native retry queue:
 * a lost answer remains pending here and JS takes over the same key after expiry. */
- (void)publishStop:(NSDictionary *)status {
 _stopDisposition=status;if(self.onUserMutation)self.onUserMutation(status);
}
- (BOOL)stopAuthorized:(NSDictionary *)entry {
 NSError *error=nil;id allowed=[PorticoQueuePlaybackStore userMutationTransaction:@"authorizeStop" payload:@{@"entry":entry} error:&error];if(error)[self publishStop:@{@"key":entry[@"key"],@"state":@"pending",@"code":@"mutation_store_unavailable"}];return [allowed boolValue];
}
- (void)finishStop:(NSDictionary *)entry change:(NSDictionary *)change {
 NSError *error=nil;
 id saved=[PorticoQueuePlaybackStore userMutationTransaction:@"finishStop" payload:@{@"key":entry[@"key"],@"leaseOwner":entry[@"leaseOwner"],@"change":change} error:&error];
 NSString *state=saved&&[saved boolValue]?change[@"state"]:@"pending";
 [self publishStop:@{@"key":entry[@"key"],@"intentId":entry[@"intentId"],@"state":state,@"code":error?@"mutation_store_unavailable":[saved boolValue]?(change[@"errorCode"]?:@""):@"mutation_pending",@"receipt":[saved boolValue]?(change[@"receipt"]?:NSNull.null):NSNull.null,@"terminalProof":[saved boolValue]?(change[@"terminalProof"]?:NSNull.null):NSNull.null}];
}
- (double)stopRetryFloor:(NSDictionary *)headers {
 double delay=1000;NSString *retry=nil;for(NSString *field in headers)if([field caseInsensitiveCompare:@"Retry-After"]==NSOrderedSame)retry=headers[field];
 if(retry){NSScanner *scan=[NSScanner scannerWithString:retry];long long seconds=0;if([scan scanLongLong:&seconds]&&scan.isAtEnd&&seconds>=0)delay=MAX(delay,seconds*1000.0);else{NSDateFormatter *f=[NSDateFormatter new];f.locale=[[NSLocale alloc]initWithLocaleIdentifier:@"en_US_POSIX"];f.timeZone=[NSTimeZone timeZoneForSecondsFromGMT:0];f.dateFormat=@"EEE, dd MMM yyyy HH:mm:ss zzz";NSDate *date=[f dateFromString:retry];if(date)delay=MAX(delay,date.timeIntervalSince1970*1000-(_mutationIssuedBaseMs+(Mono()-_mutationClockMono)*1000));}}
 return delay;
}
- (void)lookupStop:(NSDictionary *)entry token:(NSString *)token origin:(NSString *)origin floor:(double)floor {
 if(![self stopAuthorized:entry]){[self publishStop:@{@"key":entry[@"key"],@"state":@"quarantined",@"code":@"mutation_scope_changed"}];return;}
 PorticoListeningRequest *request=[PorticoListeningRequest new];request.timeout=10;request.byteLimit=65536;
 __weak PorticoListeningRequest *weak=request;
 request.completion=^(NSInteger status,id value,double started){
  NSDictionary *raw=V1Dictionary(value);if(V1Dictionary(raw[@"data"]))raw=raw[@"data"];
  NSArray *statuses=@[@"applied",@"already_stopped",@"deleted",@"removed",@"partial",@"conflict",@"invalid",@"terminal"];
  BOOL fields=YES;for(NSString *field in @[@"revision",@"generation",@"positionMs"])if(raw[field]&&!ListeningNumber(raw[field],0,9007199254740991.0,YES))fields=NO;
  if(raw[@"generation"]&&[raw[@"generation"] doubleValue]>2147483647)fields=NO;
  if(raw[@"targetId"]&&(!Text(raw[@"targetId"])||[raw[@"targetId"] lengthOfBytesUsingEncoding:NSUTF8StringEncoding]>128))fields=NO;
  if(raw[@"positionStatus"]&&![@[@"",@"applied",@"already_stopped",@"not_renderer",@"policy_rejected",@"unchanged"] containsObject:raw[@"positionStatus"]])fields=NO;
  if(fields&&status>=200&&status<300&&[raw[@"key"] isEqual:entry[@"key"]]&&[raw[@"operation"] isEqual:@"stop_playback_session"]&&[statuses containsObject:raw[@"status"]]&&ListeningNumber(raw[@"committedMs"],0,9007199254740991.0,YES)&&ListeningNumber(raw[@"expiresMs"],[raw[@"committedMs"] doubleValue],9007199254740991.0,YES)){
   NSMutableDictionary *receipt=[NSMutableDictionary new];for(NSString *field in @[@"key",@"operation",@"status",@"targetId",@"revision",@"generation",@"positionMs",@"positionStatus",@"committedMs",@"expiresMs"])if(raw[field])receipt[field]=raw[field];
   BOOL rejected=[@[@"partial",@"conflict",@"invalid",@"terminal"] containsObject:raw[@"status"]]||[raw[@"positionStatus"] isEqual:@"policy_rejected"];
   [self finishStop:entry change:@{@"state":rejected?@"review":@"confirmed",@"receipt":receipt,@"errorCode":[raw[@"positionStatus"] isEqual:@"policy_rejected"]?@"final_position_rejected":rejected?@"mutation_needs_review":@""}];return;
  }
  NSString *code=V1ErrorCode(value);
  if(status>=400&&status<500&&status!=429&&!(status==404&&[code isEqual:@"receipt_unknown"])){
   [self finishStop:entry change:@{@"state":(status==401||status==403)?@"quarantined":@"review",@"errorCode":code.length?code:@"mutation_needs_review"}];return;
  }
  double delay=MAX(floor,[self stopRetryFloor:weak.responseHeaders]);
  [self finishStop:entry change:@{@"state":@"pending",@"dueMs":@(MIN(9007199254740991.0,Wall()*1000+delay)),@"errorCode":@"mutation_pending"}];
 };
 [request start:[NSURL URLWithString:[origin stringByAppendingFormat:@"/v1/user-mutations/%@",entry[@"key"]]] token:token body:nil method:@"GET"];
}
- (NSDictionary *)reserveStop:(NSString *)owner position:(double)position {
 NSDictionary *scope=_mutationScope;NSString *session=_session[@"id"];id generation=_session[@"presentation"][@"generation"];
 if(!scope||!V1ID(session)||!ListeningNumber(generation,0,9007199254740991.0,YES)){[self publishStop:@{@"state":@"not_queued",@"code":@"mutation_scope_changed"}];return nil;}
 uint8_t random[16];if(SecRandomCopyBytes(kSecRandomDefault,sizeof(random),random)!=errSecSuccess){[self publishStop:@{@"state":@"not_queued",@"code":@"mutation_random_unavailable"}];return nil;}
 NSMutableString *nonce=[NSMutableString new];for(NSUInteger i=0;i<16;i++)[nonce appendFormat:@"%02x",random[i]];
 long long now=llround(Wall()*1000),issued=llround(_mutationIssuedBaseMs+(Mono()-_mutationClockMono)*1000);NSString *key=[NSString stringWithFormat:@"p251_%lld_%@",issued,nonce],*identity=[NSString stringWithFormat:@"stop:%@:%@",session,generation],*path=[@"/v1/playback/sessions/" stringByAppendingString:session];
 NSDictionary *engine=[_controller listeningSnapshot],*binding=V1Dictionary(engine[@"binding"]);
 BOOL evidence=[binding[@"sessionId"] isEqual:session]&&[binding[@"generation"] isEqual:generation]&&[engine[@"observed"] boolValue]&&![engine[@"seeking"] boolValue];
 NSDictionary *body=evidence?@{@"generation":generation,@"positionMs":@((long long)llround(MAX(0,isfinite(position)?position:0)*1000))}:@{@"generation":generation};
 NSDictionary *input=@{@"operation":@"stop_playback_session",@"method":@"DELETE",@"path":path,@"target":identity,@"body":body,@"headers":@{}};
 NSString *fingerprint=[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:input options:(NSJSONWritingSortedKeys|NSJSONWritingWithoutEscapingSlashes) error:nil] encoding:NSUTF8StringEncoding];
 NSDictionary *entry=@{@"nativeStopOwner":owner,@"key":key,@"operation":@"stop_playback_session",@"method":@"DELETE",@"path":path,@"target":identity,@"intentId":identity,@"fingerprint":fingerprint,@"scope":scope,@"body":body,@"headers":@{@"Idempotency-Key":key},@"state":@"pending",@"attempts":@1,@"dueMs":@(now),@"leaseOwner":[@"native:" stringByAppendingString:key],@"leaseUntilMs":@(now+30000)};
 NSError *error=nil;NSDictionary *saved=[PorticoQueuePlaybackStore userMutationTransaction:@"reserveStop" payload:@{@"entry":entry} error:&error];
 if(!saved){[self publishStop:@{@"state":@"not_queued",@"key":key,@"code":@"mutation_not_queued",@"draft":entry}];return nil;}
 [self publishStop:@{@"key":saved[@"key"],@"intentId":identity,@"state":saved[@"state"],@"code":saved[@"errorCode"]?:@"mutation_pending",@"receipt":saved[@"receipt"]?:NSNull.null}];
 return [saved[@"key"] isEqual:key]&&saved[@"leaseOwner"]?saved:nil;
}
- (void)sendStop:(NSDictionary *)entry token:(NSString *)token origin:(NSString *)origin ready:(BOOL)ready {
 if(!ready||!token.length){[self finishStop:entry change:@{@"state":@"pending",@"errorCode":@"mutation_pending"}];return;}
 if(![self stopAuthorized:entry])return;
 PorticoListeningRequest *request=[PorticoListeningRequest new];request.timeout=10;request.headers=entry[@"headers"];request.byteLimit=65536;
 __weak PorticoListeningRequest *weak=request;
 request.completion=^(NSInteger status,id value,double started){
  if(status>=400&&status<500&&status!=429){NSMutableDictionary *change=[@{@"state":(status==401||status==403)?@"quarantined":@"review",@"errorCode":V1ErrorCode(value).length?V1ErrorCode(value):@"mutation_needs_review"} mutableCopy];if([@[@400,@409,@412,@422] containsObject:@(status)])change[@"terminalProof"]=@{@"kind":@"refused",@"httpStatus":@(status),@"code":change[@"errorCode"]};[self finishStop:entry change:change];return;}
  // A response alone is never the persisted result; even a timeout is looked up.
  [self lookupStop:entry token:token origin:origin floor:[self stopRetryFloor:weak.responseHeaders]];
 };
 [request start:[NSURL URLWithString:[origin stringByAppendingString:entry[@"path"]]] token:token body:entry[@"body"] method:@"DELETE"];
}
- (NSDictionary *)stopDisposition {return _stopDisposition;}
/** A known Stop result is consumed only after this occurrence is no longer a
 * local native owner. This also re-proves consumption after JS/app restart. */
- (BOOL)consumeStop:(NSString *)key error:(NSError **)error {
 NSDictionary *state=[PorticoQueuePlaybackStore userMutationTransaction:@"read" payload:@{} error:error];if(!state)return NO;
 NSDictionary *entry=nil;for(NSDictionary *e in state[@"entries"])if([e[@"key"] isEqual:key]){entry=e;break;}
 if(!Text(entry[@"nativeStopOwner"])||![entry[@"operation"] isEqual:@"stop_playback_session"])return NO;
 if(_owner){for(NSDictionary *session in @[_session?:@{},_audible?:@{}]){NSString *target=[NSString stringWithFormat:@"stop:%@:%@",session[@"id"],session[@"presentation"][@"generation"]];if([target isEqual:entry[@"target"]])return NO;}}
 id saved=[PorticoQueuePlaybackStore userMutationTransaction:@"consumeStop" payload:@{@"key":key,@"owner":entry[@"nativeStopOwner"]} error:error];
 if([saved boolValue]&&self.onUserMutation)self.onUserMutation(@{@"key":key,@"state":entry[@"state"],@"nativeStopConsumed":@YES});
 return [saved boolValue];
}


- (void)retire:(NSString *)owner stop:(BOOL)stop {
 if(![_owner isEqual:owner])return;
 NSString *origin=_origin,*token=_token,*session=_session[@"id"];BOOL ready=_routeReady;
 double position=[[_controller listeningSnapshot][@"positionSeconds"] doubleValue];
 NSDictionary *stopEntry=stop&&!_terminal?[self reserveStop:owner position:position]:nil;
 // Fence locally before any network cleanup: nothing below can play or publish.
 _safetyPaused=YES;[_metadataRequest cancel];_metadataRequest=nil;[self clearArtwork];[_timer invalidate];_timer=nil;[_request cancel];_request=nil;_busy=NO;
 [_controller clearListeningOwner:owner];[self removeRemoteCommands];MPNowPlayingInfoCenter.defaultCenter.nowPlayingInfo=nil;
 if(_journalKey&&!_commitWanted)[PorticoQueuePlaybackStore write:_journalKey value:@{@"version":@1,@"sessionId":session?:NSNull.null,@"retired":@YES} error:nil];
 [self endControlGrace];
 _owner=nil;_token=nil;_actions=nil;_transition=nil;_metadata=nil;_prepared=nil;_committed=nil;_audible=nil;_policy=nil;_timerCustody=nil;_commitWanted=NO;
 if(stop&&Text(_stopDisposition[@"key"])) {NSError *error=nil;[self consumeStop:_stopDisposition[@"key"] error:&error];if(error)[self publishStop:@{@"key":_stopDisposition[@"key"],@"state":@"pending",@"code":@"mutation_store_unavailable"}];}
 if(stopEntry)[self sendStop:stopEntry token:token origin:origin ready:ready];
 // Not while the other player engine has started a video: the session is its to keep.
 [[AVAudioSession sharedInstance] setActive:NO withOptions:AVAudioSessionSetActiveOptionNotifyOthersOnDeactivation error:nil];
}
- (void)invalidate {if(_owner)[self retire:_owner stop:YES];_invalidated=YES;self.onSnapshot=nil;[NSNotificationCenter.defaultCenter removeObserver:self];}
- (void)dealloc {[_timer invalidate];[_request cancel];[_artworkRequest cancel];[_metadataRequest cancel];[NSNotificationCenter.defaultCenter removeObserver:self];}
@end

#pragma mark - Exit-test harness (plan §9.6; Debug builds only)
#if DEBUG
/*
 * Drives PorticoListeningSessionOwner against a FakePlaybackServer over HTTP
 * (packages/client-core/tools/fake-playback-http.ts), with no JS and no sign-in: it does what the
 * JS side would (create the queue, hand the session over, attach the engine lease) and records what
 * the owner, the engine and the server did. Launch with PORTICO_LISTENING_HARNESS=<fake origin> and
 * PORTICO_LISTENING_HARNESS_OUT=<json path>; optional PORTICO_LISTENING_HARNESS_ONLY=<scenario,…>.
 */
@interface PorticoListeningSessionOwner (Harness)
- (void)remote:(NSString *)action value:(NSNumber *)value owner:(NSString *)installedOwner;
@end
@interface PorticoListeningHarness : NSObject
@end
@implementation PorticoListeningHarness {
 NSString *_origin,*_out;NSSet *_only;PorticoPlaybackController *_controller;PorticoListeningSessionOwner *_owner;
 // Against a real server (the runner e2e server): a bearer token and the library's item ids from
 // PORTICO_LISTENING_HARNESS_CONFIG; no /__test controls, so verdicts come from the engine and owner.
 BOOL _real;NSDictionary *_config;NSMutableDictionary *_plans;
 // Book cases: the session's resumed start and the viewer's book rate.
 double _startMs,_bookRate;
 NSMutableArray *_results;NSMutableArray *_facts,*_snapshots;NSDictionary *_last,*_base;NSArray *_queue;NSInteger _serial;
}
static PorticoListeningHarness *harness;
+ (void)load {
 NSDictionary *env=NSProcessInfo.processInfo.environment;
 if(![env[@"PORTICO_LISTENING_HARNESS"] length]||![env[@"PORTICO_LISTENING_HARNESS_OUT"] length])return;
 dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(3*NSEC_PER_SEC)),dispatch_get_main_queue(),^{harness=[PorticoListeningHarness new];[harness run];});
}
- (void)request:(NSString *)method path:(NSString *)path body:(id)body headers:(NSDictionary *)headers done:(void (^)(NSInteger,id))done {
 NSMutableURLRequest *r=[NSMutableURLRequest requestWithURL:[NSURL URLWithString:[_origin stringByAppendingString:path]]];r.HTTPMethod=method;
 if(body){r.HTTPBody=[NSJSONSerialization dataWithJSONObject:body options:0 error:nil];[r setValue:@"application/json" forHTTPHeaderField:@"Content-Type"];}
 for(NSString *k in headers)[r setValue:headers[k] forHTTPHeaderField:k];
 if(_real)[r setValue:[@"Bearer " stringByAppendingString:_config[@"accessToken"]] forHTTPHeaderField:@"Authorization"];
 [[NSURLSession.sharedSession dataTaskWithRequest:r completionHandler:^(NSData *data,NSURLResponse *response,NSError *error){
  id value=data.length?[NSJSONSerialization JSONObjectWithData:data options:0 error:nil]:nil;NSInteger status=[(NSHTTPURLResponse *)response statusCode];
  dispatch_async(dispatch_get_main_queue(),^{done(error?0:status,value);});}] resume];
}
- (void)wait:(BOOL (^)(void))condition timeout:(double)seconds then:(void (^)(BOOL))next {
 [self poll:condition until:NSProcessInfo.processInfo.systemUptime+seconds then:next];
}
- (void)poll:(BOOL (^)(void))condition until:(double)until then:(void (^)(BOOL))next {
 if(condition()){next(YES);return;}
 if(NSProcessInfo.processInfo.systemUptime>=until){next(NO);return;}
 dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(0.1*NSEC_PER_SEC)),dispatch_get_main_queue(),^{[self poll:condition until:until then:next];});
}
- (void)after:(double)seconds then:(void (^)(void))next {dispatch_after(dispatch_time(DISPATCH_TIME_NOW,(int64_t)(seconds*NSEC_PER_SEC)),dispatch_get_main_queue(),next);}
- (void)run {
 NSDictionary *env=NSProcessInfo.processInfo.environment;_origin=env[@"PORTICO_LISTENING_HARNESS"];_out=env[@"PORTICO_LISTENING_HARNESS_OUT"];
 _only=[env[@"PORTICO_LISTENING_HARNESS_ONLY"] length]?[NSSet setWithArray:[env[@"PORTICO_LISTENING_HARNESS_ONLY"] componentsSeparatedByString:@","]]:nil;
 _results=[NSMutableArray new];
 NSString *config=env[@"PORTICO_LISTENING_HARNESS_CONFIG"];
 if(config.length){NSData *data=[NSData dataWithContentsOfFile:config];_config=data?[NSJSONSerialization JSONObjectWithData:data options:0 error:nil]:nil;_real=[_config[@"accessToken"] isKindOfClass:NSString.class];
  if(_real){_queue=_config[@"scenarios"];[self next:0];return;}}
 _queue=@[@"gapless",@"crossfade",@"cancel",@"fallback-prepare-409",@"fallback-prepare-503",@"fallback-prepare-offline",@"fallback-commit-409",@"fallback-commit-410",@"fallback-commit-offline",@"commit-lost-once",@"commit-answer-lost",@"skip-in-drain",@"crossfade-rate",@"route-change",@"route-change-edge",@"shared-cache-readers",@"same-album",@"album-skip",@"book-resume",@"book-rate",@"book-skip",@"book-chapter-timer",@"book-parts",@"terminate",@"lease"];
 [self next:0];
}
- (void)save {
 NSData *json=[NSJSONSerialization dataWithJSONObject:@{@"results":_results,@"finished":@(_results.count>=_queue.count)} options:NSJSONWritingPrettyPrinted|NSJSONWritingSortedKeys error:nil];
 [json writeToFile:_out atomically:YES];
}
- (void)next:(NSUInteger)i {
 while(i<_queue.count&&_only&&![_only containsObject:_queue[i]])i++;
 if(i>=_queue.count){[self save];NSLog(@"[harness] done");return;}
 NSString *name=_queue[i];NSLog(@"[harness] %@",name);_startMs=0;_bookRate=1;
 PorticoListeningHarness *me=self;
 [self request:(_real?@"GET":@"POST") path:(_real?@"/v1/capabilities":@"/__test/reset") body:(_real?nil:@{}) headers:nil done:^(NSInteger s,id v){[me state:^(NSDictionary *base){me->_base=base;
  [me scenario:name done:^(NSDictionary *result){PorticoListeningHarness *self=me;(void)self;
   NSMutableDictionary *r=[result mutableCopy];r[@"scenario"]=name;[self->_results addObject:r];[self save];
   if(self->_owner.activeOwner)[self->_owner retire:self->_owner.activeOwner stop:YES];
   [self after:1.5 then:^{[me next:i+1];}];
  }];
 }];}];
}
/** Creates a queue that starts playing and hands its session to a fresh owner, as JS would. */
- (void)start:(id)items effects:(NSDictionary *)effects seek:(double)seek done:(void (^)(NSDictionary *session))done {
 NSString *key=[NSString stringWithFormat:@"harness-%ld-%@",(long)++_serial,NSUUID.UUID.UUIDString];
 PorticoListeningHarness *me=self;
 // A list of item ids, or a whole source (a container: the 1M collection).
 NSDictionary *source=[items isKindOfClass:NSDictionary.class]?items:@{@"items":@{@"ids":items}};
 [self request:@"POST" path:@"/v1/queues" body:@{@"owner":@"device",@"segments":@[@{@"source":source}],@"startPlayback":_startMs>0?@{@"state":@"playing",@"startPositionMs":@(_startMs)}:@{@"state":@"playing"}} headers:@{@"Idempotency-Key":key} done:^(NSInteger status,id value){
  PorticoListeningHarness *self=me;(void)self;NSDictionary *session=value[@"session"],*queue=value[@"queue"];
  if(status!=201&&status!=200){NSLog(@"[harness] queue create %ld %@",(long)status,value);done(nil);return;}
  self->_controller=[PorticoPlaybackController new];
  self->_owner=[[PorticoListeningSessionOwner alloc] initWithController:self->_controller render:[PorticoListeningDecodeRender new]];
  self->_facts=[NSMutableArray new];self->_snapshots=[NSMutableArray new];self->_last=nil;
  PorticoListeningSessionOwner *owner=self->_owner;NSMutableArray *facts=self->_facts,*snapshots=self->_snapshots;
  self->_controller.onFact=^(NSDictionary *fact){
   if(![@[@"progress",@"rateStatus"] containsObject:fact[@"type"]]){NSMutableDictionary *f=[NSMutableDictionary new];for(NSString *k in @[@"type",@"token",@"gapFrames",@"tailFrames",@"chained",@"fadeSeconds",@"overlapSeconds",@"planFrames",@"decodedFrames",@"position",@"error"])if(fact[k])f[k]=fact[k];f[@"session"]=fact[@"binding"][@"sessionId"]?:@"";f[@"at"]=@(NSProcessInfo.processInfo.systemUptime);f[@"background"]=@(UIApplication.sharedApplication.applicationState==UIApplicationStateBackground);[facts addObject:f];}
   [owner fact:fact];};
  owner.onSnapshot=^(NSDictionary *s){PorticoListeningHarness *h=me;h->_last=s;
   // Each session's plan as the server sent it (exit tests (a) and (h)).
   NSDictionary *plan=s[@"session"][@"presentation"][@"audioRender"];NSString *sid=s[@"session"][@"id"];
   if(h->_real&&[sid isKindOfClass:NSString.class]&&!h->_plans[sid])h->_plans[sid]=@{@"itemId":s[@"session"][@"itemId"]?:@"",@"mode":plan[@"mode"]?:@"none",@"durationFrames":plan[@"durationFrames"]?:@0,@"sampleRate":plan[@"sampleRate"]?:@0,@"codec":plan[@"codec"]?:@"",@"reason":plan[@"reason"]?:@"",@"noticeCode":s[@"noticeCode"]?:@"",@"error":s[@"error"]?:@""};
   else if(h->_real&&[sid isKindOfClass:NSString.class]&&[s[@"noticeCode"] length]&&![h->_plans[sid][@"noticeCode"] length]){NSMutableDictionary *p=[h->_plans[sid] mutableCopy];p[@"noticeCode"]=s[@"noticeCode"];h->_plans[sid]=p;}
   NSDictionary *prev=snapshots.lastObject;if(![prev[@"session"] isEqual:s[@"session"][@"id"]]||![prev[@"error"] isEqual:s[@"error"]]||![prev[@"terminal"] isEqual:s[@"terminal"]]||![prev[@"intent"] isEqual:s[@"intent"]])
    [snapshots addObject:@{@"session":s[@"session"][@"id"]?:@"",@"item":s[@"session"][@"itemId"]?:@"",@"intent":s[@"intent"]?:@"",@"error":s[@"error"]?:NSNull.null,@"terminal":s[@"terminal"]?:@NO,@"end":s[@"end"]?:NSNull.null,@"at":@(NSProcessInfo.processInfo.systemUptime)}];};
  NSString *ownerId=[NSString stringWithFormat:@"harness%ld",(long)self->_serial];
  NSDictionary *policy=@{@"revision":@1,@"activityRevision":@1,@"activityAtMs":@((long long)(Wall()*1000)),@"timerRevision":@0,@"effects":effects,@"preferences":@{@"bookRate":@(self->_bookRate>0?self->_bookRate:1),@"passoutMinutes":@0},@"timer":NSNull.null};
  NSError *error=nil;
  self->_plans=[NSMutableDictionary new];
  BOOL adopted=[owner adopt:@{@"owner":ownerId,@"origin":self->_origin,@"accessToken":self->_real?self->_config[@"accessToken"]:@"harness-token",@"principalKey":[NSString stringWithFormat:@"harness-%ld",(long)self->_serial],@"session":session,@"queueId":queue[@"id"],@"queueRevision":queue[@"revision"],@"durationSeconds":@0,@"lastSeq":@0,@"leaseMs":@120000,@"transitions":@YES,@"intent":@"playing",@"policy":policy} error:&error];
  if(!adopted){NSLog(@"[harness] adopt failed %@",error);done(nil);return;}
  // What NativePlaybackCoordinator does: take the engine lease and attach it to the owner's session.
  NSDictionary *lease=[self->_controller acquire:@"harness"];
  [self->_controller acceptCommand:@{@"binding":@{@"lease":lease[@"lease"],@"intentId":@1,@"sessionId":session[@"id"],@"generation":session[@"presentation"][@"generation"],@"itemId":session[@"itemId"]},@"listeningOwner":ownerId,@"commandRevision":@1}];
  if(seek>0)[owner action:@"seek" value:@(seek) owner:ownerId];
  done(session);
 }];
}
- (void)state:(void (^)(NSDictionary *))done {if(_real){done(@{});return;}[self request:@"GET" path:@"/__test/state" body:nil headers:nil done:^(NSInteger s,id v){done([v isKindOfClass:NSDictionary.class]?v:@{});}];}
/** What changed on the server during this scenario (the fake's counters span the whole run). */
- (NSInteger)delta:(NSDictionary *)state key:(NSString *)key {return [state[key] integerValue]-[_base[key] integerValue];}
- (NSArray *)newSessions:(NSDictionary *)state item:(NSString *)item {
 NSSet *before=[NSSet setWithArray:[_base[@"sessions"] valueForKey:@"id"]?:@[]];
 return [[self sessionsFor:state item:item] filteredArrayUsingPredicate:[NSPredicate predicateWithBlock:^BOOL(NSDictionary *s,NSDictionary *b){return ![before containsObject:s[@"id"]];}]];
}
- (NSInteger)newAdvances:(NSDictionary *)state {
 NSUInteger seen=[_base[@"log"] count];NSArray *log=state[@"log"];NSInteger n=0;double since=[[_base[@"log"] lastObject][@"at"] doubleValue];
 for(NSDictionary *e in log)if([e[@"at"] doubleValue]>since&&[e[@"path"] containsString:@":advance"])n++;(void)seen;return n;
}
- (NSArray *)boundaries {return [_facts filteredArrayUsingPredicate:[NSPredicate predicateWithFormat:@"type == 'audioBoundary'"]];}
- (NSArray *)sessionsFor:(NSDictionary *)state item:(NSString *)item {return [state[@"sessions"] filteredArrayUsingPredicate:[NSPredicate predicateWithFormat:@"itemId == %@",item]];}
/** Every session's timeline seq rises strictly; at most one session is live. */
- (NSDictionary *)timelineChecks:(NSDictionary *)state {
 BOOL rising=YES;NSInteger live=0,reports=0;
 for(NSDictionary *s in state[@"sessions"]){long long last=0;for(NSDictionary *r in s[@"reports"]){long long seq=[r[@"seq"] longLongValue];if(seq<=last)rising=NO;last=seq;reports++;}if(![s[@"ended"] boolValue])live++;}
 return @{@"seqStrictlyRising":@(rising),@"liveSessions":@(live),@"reports":@(reports)};
}
static double HarnessFootprintMB(void){task_vm_info_data_t info;mach_msg_type_number_t count=TASK_VM_INFO_COUNT;if(task_info(mach_task_self(),TASK_VM_INFO,(task_info_t)&info,&count)!=KERN_SUCCESS)return -1;return info.phys_footprint/1048576.0;}
- (NSArray *)ids:(NSArray *)names {NSMutableArray *out=[NSMutableArray new];for(NSString *n in names){NSString *i=_config[@"items"][n];if(i)[out addObject:i];}return out;}
/** Frame exactness per finished track: the plan's durationFrames against the sidecar's, and what
 * the engine decoded against the plan (exit test (a), on the real server's facts). */
- (NSDictionary *)frameChecks:(NSArray *)boundaries names:(NSArray *)names {
 NSMutableArray *rows=[NSMutableArray new];BOOL exact=YES;
 for(NSUInteger k=0;k<boundaries.count&&k<names.count;k++){NSDictionary *b=boundaries[k];NSNumber *sidecar=_config[@"frames"][names[k]];
  // A crossfade cuts the old deck's tail on purpose, so frames are compared only on joins.
  BOOL faded=[b[@"fadeSeconds"] doubleValue]>0;
  BOOL ok=[b[@"planFrames"] longLongValue]>0&&(!sidecar||[sidecar isEqual:b[@"planFrames"]])&&(faded||([b[@"decodedFrames"] isEqual:b[@"planFrames"]]&&[b[@"tailFrames"] longLongValue]==0&&(![b[@"chained"] boolValue]||[b[@"gapFrames"] longLongValue]==0)));
  exact=exact&&ok;[rows addObject:@{@"track":names[k],@"planFrames":b[@"planFrames"]?:@0,@"decodedFrames":b[@"decodedFrames"]?:@0,@"sidecarFrames":sidecar?:NSNull.null,@"gapFrames":b[@"gapFrames"]?:@0,@"tailFrames":b[@"tailFrames"]?:@0,@"chained":b[@"chained"]?:@NO,@"fadeSeconds":b[@"fadeSeconds"]?:@0,@"overlapSeconds":b[@"overlapSeconds"]?:@0,@"ok":@(ok)}];}
 return @{@"tracks":rows,@"exact":@(exact&&boundaries.count+1>=names.count)};
}
/** Audiobooks under the native owner: 2-minute parts `book-1`, `book-2` of one book (`audiobook_file`). */
- (double)enginePosition {return [[_controller listeningSnapshot][@"positionSeconds"] doubleValue];}
- (NSArray *)reportsFor:(NSDictionary *)state session:(NSString *)sid {for(NSDictionary *s in state[@"sessions"])if([s[@"id"] isEqual:sid])return s[@"reports"]?:@[];return @[];}
- (NSDictionary *)sessionIn:(NSDictionary *)state id:(NSString *)sid {for(NSDictionary *s in state[@"sessions"])if([s[@"id"] isEqual:sid])return s;return nil;}
- (void)bookScenario:(NSString *)name gapless:(NSDictionary *)gapless done:(void (^)(NSDictionary *))done {
 PorticoListeningHarness *me=self;
 if([name isEqual:@"book-resume"]){
  // Resume on the book file at its saved position (60 s); no report may carry an earlier position
  // (that would overwrite the bookmark); pause reports where it paused; stop saves it (DELETE).
  _startMs=60000;
  [self start:@[@"book-1"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   NSString *sid=session[@"id"];
   [me wait:^BOOL{return [[me->_controller listeningSnapshot][@"observed"] boolValue]&&[me enginePosition]>0;} timeout:20 then:^(BOOL started){
    double first=[me enginePosition];
    [me after:12 then:^{
     [me->_owner action:@"pause" value:nil owner:me->_owner.activeOwner];
     [me after:2 then:^{double paused=[me enginePosition];
      [me->_owner retire:me->_owner.activeOwner stop:YES];
      [me after:2 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;NSArray *reports=[self reportsFor:st session:sid];NSDictionary *sess=[self sessionIn:st id:sid];
       long long lowest=LLONG_MAX;for(NSDictionary *r in reports)lowest=MIN(lowest,[r[@"positionMs"] longLongValue]);
       NSDictionary *last=reports.lastObject;double saved=[sess[@"positionMs"] doubleValue]/1000.0;
       BOOL pausedReport=NO;for(NSDictionary *r in reports)if([r[@"state"] isEqual:@"paused"]&&fabs([r[@"positionMs"] doubleValue]/1000.0-paused)<=1)pausedReport=YES;
       done(@{@"started":@(started),@"firstPosition":@(first),@"pausedAt":@(paused),@"lowestReportedMs":@(lowest==LLONG_MAX?-1:lowest),@"reports":@(reports.count),@"lastReport":last?:NSNull.null,@"savedOnStop":@(saved),@"ended":sess[@"ended"]?:@NO,
        @"pass":@(started&&first>=59.5&&first<63&&lowest>=59000&&pausedReport&&[sess[@"ended"] boolValue]&&fabs(saved-paused)<=1.5)});}];}];
     }];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"book-rate"]){
  // The viewer's book rate (1.5x) applies to a book file, and the timeline says so.
  _bookRate=1.5;
  [self start:@[@"book-1"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   NSString *sid=session[@"id"];
   [me wait:^BOOL{return fabs([[me->_controller listeningSnapshot][@"effectiveRate"] doubleValue]-1.5)<.01;} timeout:20 then:^(BOOL rated){
    double a=[me enginePosition];
    [me after:4 then:^{double b=[me enginePosition];
     [me after:8 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;BOOL reportedRate=NO;for(NSDictionary *r in [self reportsFor:st session:sid])if(fabs([r[@"rate"] doubleValue]-1.5)<.01)reportedRate=YES;
      done(@{@"rated":@(rated),@"advancedIn4s":@(b-a),@"reportedRate":@(reportedRate),@"pass":@(rated&&fabs((b-a)-6)<=1&&reportedRate)});}];}];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"book-skip"]){
  // Lock-screen skips at the viewer's intervals: forward 30 s, back 15 s.
  _startMs=40000;
  [self start:@[@"book-1"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me->_owner skipBack:15 forward:30];
   [me wait:^BOOL{return [[me->_controller listeningSnapshot][@"observed"] boolValue]&&[me enginePosition]>40.5;} timeout:20 then:^(BOOL playing){
    double p0=[me enginePosition];[me->_owner remote:@"skip" value:@30 owner:me->_owner.activeOwner];
    [me wait:^BOOL{return [me enginePosition]>=p0+29;} timeout:10 then:^(BOOL forward){
     double p1=[me enginePosition];[me->_owner remote:@"skip" value:@-15 owner:me->_owner.activeOwner];
     [me wait:^BOOL{return [me enginePosition]<p1-10;} timeout:10 then:^(BOOL back){
      [me after:1 then:^{double p2=[me enginePosition];
       done(@{@"playing":@(playing),@"before":@(p0),@"afterForward":@(p1),@"afterBack":@(p2),@"pass":@(playing&&forward&&back&&fabs(p1-p0-30)<=1.5&&fabs(p2-(p1-15))<=2)});}];
     }];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"book-chapter-timer"]){
  // A sleep timer to the end of this chapter pauses there (chapter end 5 s ahead).
  [self start:@[@"book-1"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [[me->_controller listeningSnapshot][@"observed"] boolValue]&&[me enginePosition]>1;} timeout:20 then:^(BOOL playing){
    double end=[me enginePosition]+5;NSDictionary *s=me->_last[@"session"];
    NSDictionary *policy=@{@"revision":@2,@"activityRevision":@1,@"activityAtMs":@((long long)(Wall()*1000)),@"timerRevision":@1,@"effects":gapless,@"preferences":@{@"bookRate":@1,@"passoutMinutes":@0},
     @"timer":@{@"label":@"End of chapter",@"libraryId":@"library",@"choice":@"chapter",@"itemId":s[@"itemId"]?:@"book-1",@"sessionId":s[@"id"]?:@"",@"generation":s[@"presentation"][@"generation"]?:@1,@"endSeconds":@(end)}};
    BOOL accepted=[me->_owner policy:policy owner:me->_owner.activeOwner];
    [me wait:^BOOL{return [me->_last[@"intent"] isEqual:@"paused"];} timeout:15 then:^(BOOL paused){
     [me after:1 then:^{double at=[me enginePosition];
      done(@{@"playing":@(playing),@"accepted":@(accepted),@"chapterEnd":@(end),@"pausedAt":@(at),@"paused":@(paused),@"pass":@(playing&&accepted&&paused&&at>=end-0.6&&at<=end+1.5)});}];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"book-parts"]){
  // The next part of the book joins gaplessly (one commit, gap 0), 10 s before the end of part 1.
  _startMs=110000;
  [self start:@[@"book-1",@"book-2"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [me boundaries].count>=1;} timeout:40 then:^(BOOL ok){
    [me after:3 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;NSDictionary *b=[self boundaries].firstObject;
     done(@{@"boundary":b?:NSNull.null,@"commits":@([self delta:st key:@"commits"]),@"nowPlaying":self->_last[@"session"][@"itemId"]?:@"",
      @"pass":@(ok&&[b[@"chained"] boolValue]&&[b[@"gapFrames"] longLongValue]==0&&[self delta:st key:@"commits"]==1&&[self->_last[@"session"][@"itemId"] isEqual:@"book-2"])});}];}];
   }];
  }];return;
 }
 done(@{@"error":@"unknown book case"});
}
- (void)realScenario:(NSString *)name done:(void (^)(NSDictionary *))done {
 PorticoListeningHarness *me=self;
 NSDictionary *spec=_config[@"cases"][name];NSArray *names=spec[@"tracks"];NSDictionary *effects=spec[@"effects"]?:@{@"gapless":@YES,@"crossfadeSeconds":@0,@"normalization":@"off"};
 if([spec[@"kind"] isEqual:@"edges"]){
  // (a)(b)(c): tracks played through, every edge measured; `sameAlbum` expects chained joins even with a crossfade set.
  NSUInteger edges=names.count-1;double started=HarnessFootprintMB();
  [self start:[self ids:names] effects:effects seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [me boundaries].count>=edges||[me->_last[@"terminal"] boolValue];} timeout:[spec[@"timeout"] doubleValue]?:120 then:^(BOOL ok){
    [me after:2 then:^{PorticoListeningHarness *self=me;NSArray *b=[self boundaries];NSDictionary *frames=[self frameChecks:b names:names];
     // sameAlbum: every join chained with no fade although a crossfade is set; crossfade: every edge
     // fades on a second node and overlaps for the fade it chose (±50 ms).
     BOOL joins=b.count>=edges;for(NSDictionary *f in b){
      if([spec[@"sameAlbum"] boolValue])joins=joins&&[f[@"chained"] boolValue]&&[f[@"fadeSeconds"] doubleValue]==0;
      if([spec[@"crossfade"] boolValue])joins=joins&&![f[@"chained"] boolValue]&&[f[@"fadeSeconds"] doubleValue]>0&&fabs([f[@"overlapSeconds"] doubleValue]-[f[@"fadeSeconds"] doubleValue])<=.05;}
     NSMutableArray *plans=[NSMutableArray new];for(NSString *k in self->_plans)[plans addObject:self->_plans[k]];
     done(@{@"edges":@(b.count),@"expectedEdges":@(edges),@"frames":frames,@"plans":plans,@"footprintMB":@[@(started),@(HarnessFootprintMB())],@"snapshots":self->_snapshots,
      @"pass":@(ok&&joins&&[frames[@"exact"] boolValue])});}];
   }];
  }];return;
 }
 if([spec[@"kind"] isEqual:@"long"]){
  // (j): a 2-hour 24/96 FLAC. Decoded audio held stays under 60 s per deck while it plays and after a seek to the middle.
  __block double maxAhead=0,maxHeld=0,peakMB=0;double startMB=HarnessFootprintMB();
  [self start:[self ids:names] effects:effects seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   __block void (^sample)(int,void(^)(void));
   sample=^(int left,void(^then)(void)){NSDictionary *e=[me->_controller listeningSnapshot];maxAhead=MAX(maxAhead,[e[@"decodedAheadSeconds"] doubleValue]);maxHeld=MAX(maxHeld,[e[@"candidateHeldSeconds"] doubleValue]);peakMB=MAX(peakMB,HarnessFootprintMB());
    if(left<=0){then();return;}[me after:1 then:^{sample(left-1,then);}];};
   [me wait:^BOOL{return [[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue]>2;} timeout:60 then:^(BOOL playing){
    sample(20,^{double before=[[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue];
     [me->_owner action:@"seek" value:@3600 owner:me->_owner.activeOwner];
     [me wait:^BOOL{return [[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue]>3602;} timeout:60 then:^(BOOL resumed){
      sample(20,^{double after=[[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue];PorticoListeningHarness *self=me;
       NSMutableArray *plans=[NSMutableArray new];for(NSString *k in self->_plans)[plans addObject:self->_plans[k]];
       done(@{@"playing":@(playing),@"positionBeforeSeek":@(before),@"resumedAfterSeek":@(resumed),@"positionEnd":@(after),@"maxDecodedAheadSeconds":@(maxAhead),@"maxCandidateHeldSeconds":@(maxHeld),
        @"footprintMB":@{@"start":@(startMB),@"peak":@(peakMB)},@"plans":plans,@"snapshots":self->_snapshots,
        @"pass":@(playing&&resumed&&after>3610&&maxAhead<=60&&maxHeld<=60)});});
     }];
    });
   }];
  }];return;
 }
 if([spec[@"kind"] isEqual:@"container"]){
  // (i): a 1M-track queue from one container. It starts at once, moves, and the device holds a window, never the queue.
  double startMB=HarnessFootprintMB();
  [self start:spec[@"source"] effects:effects seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [me->_last[@"session"][@"state"] isEqual:@"playing"]||[[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue]>0.5;} timeout:60 then:^(BOOL playing){
    NSString *first=me->_last[@"session"][@"itemId"];
    [me->_owner advance:@"next" entry:nil owner:me->_owner.activeOwner];
    [me wait:^BOOL{NSString *now=me->_last[@"session"][@"itemId"];return now&&![now isEqual:first];} timeout:30 then:^(BOOL moved){
     [me after:5 then:^{PorticoListeningHarness *self=me;// The owner keeps the queue's identity and header only (never its entries): measure what it holds.
      NSDictionary *queue=[self->_owner valueForKey:@"_queue"],*header=[self->_owner valueForKey:@"_queueHeader"];
      NSUInteger held=[NSJSONSerialization isValidJSONObject:queue?:@{}]?[NSJSONSerialization dataWithJSONObject:queue?:@{} options:0 error:nil].length:0;
      held+=[NSJSONSerialization isValidJSONObject:header?:@{}]?[NSJSONSerialization dataWithJSONObject:header?:@{} options:0 error:nil].length:0;double endMB=HarnessFootprintMB();
      done(@{@"playing":@(playing),@"moved":@(moved),@"firstItem":first?:@"",@"nextItem":self->_last[@"session"][@"itemId"]?:@"",@"queueBytesHeld":@(held),@"queueHeader":header?:NSNull.null,
       @"footprintMB":@{@"start":@(startMB),@"end":@(endMB)},@"snapshots":self->_snapshots,
       @"pass":@(playing&&moved&&held<=16384&&endMB-startMB<100)});}];
    }];
   }];
  }];return;
 }
 if([spec[@"kind"] isEqual:@"unavailable"]){
  // (h): a plan the device can't render (`unavailable`) plays through the ordinary player, with the quiet notice and no error.
  [self start:[self ids:names] effects:effects seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue]>1.5||[me->_last[@"terminal"] boolValue];} timeout:45 then:^(BOOL played){
    [me after:1 then:^{PorticoListeningHarness *self=me;NSMutableArray *plans=[NSMutableArray new];for(NSString *k in self->_plans)[plans addObject:self->_plans[k]];
     NSDictionary *first=plans.firstObject;BOOL quiet=[first[@"mode"] isEqual:@"unavailable"]&&[first[@"noticeCode"] length]>0&&![self->_last[@"error"] isKindOfClass:NSString.class];
     done(@{@"played":@(played),@"plans":plans,@"error":self->_last[@"error"]?:NSNull.null,@"noticeCode":self->_last[@"noticeCode"]?:NSNull.null,@"snapshots":self->_snapshots,@"pass":@(played&&quiet)});}];
   }];
  }];return;
 }
 done(@{@"error":@"unknown case"});
}
- (void)scenario:(NSString *)name done:(void (^)(NSDictionary *))done {
 if(_real){[self realScenario:name done:done];return;}
 PorticoListeningHarness *me=self;
 NSDictionary *gapless=@{@"gapless":@YES,@"crossfadeSeconds":@0,@"normalization":@"off"};
 if([name isEqual:@"gapless"]){
  // (b)(f): four album tracks (2.5–3 s each), three edges, the app in the background.
  NSArray *items=@[@"fx-album-flac-01",@"fx-album-flac-02",@"fx-album-flac-03",@"fx-album-flac-04"];
  [self start:items effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [me boundaries].count>=3;} timeout:60 then:^(BOOL ok){
    [me after:4 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *b=[self boundaries];
     NSMutableDictionary *r=[@{@"boundaries":b,@"commits":@([self delta:st key:@"commits"]),@"preparations":@([self delta:st key:@"preparations"]),@"snapshots":self->_snapshots} mutableCopy];
     [r addEntriesFromDictionary:[self timelineChecks:st]];
     BOOL exact=b.count>=3;for(NSDictionary *f in b)exact=exact&&[f[@"gapFrames"] longLongValue]==0&&[f[@"tailFrames"] longLongValue]==0;
     r[@"pass"]=@(ok&&exact&&[self delta:st key:@"commits"]==3&&[r[@"seqStrictlyRising"] boolValue]&&[r[@"liveSessions"] integerValue]<=1);done(r);}];}];
   }];
  }];return;
 }
 if([name isEqual:@"crossfade"]){
  // (c): 6 s crossfade between 20 s tracks of no shared album.
  [self start:@[@"long-1",@"long-2"] effects:@{@"gapless":@NO,@"crossfadeSeconds":@6,@"normalization":@"off"} seek:6 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [me boundaries].count>=1;} timeout:45 then:^(BOOL ok){
    [me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSDictionary *b=[self boundaries].firstObject;double overlap=[b[@"overlapSeconds"] doubleValue];
     done(@{@"boundary":b?:NSNull.null,@"overlapSeconds":@(overlap),@"overlapErrorMs":@((overlap-6)*1000),@"commits":@([self delta:st key:@"commits"]),@"pass":@(ok&&fabs(overlap-6)<=.05&&[self delta:st key:@"commits"]==1)});}];
   }];
  }];return;
 }
 if([name isEqual:@"cancel"]){
  // (d): pause after the edge is prepared; no commit; the track ends and advances once.
  [self start:@[@"long-1",@"long-2"] effects:gapless seek:10 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [[me->_facts valueForKey:@"type"] containsObject:@"audioPrepared"];} timeout:20 then:^(BOOL ok){
    PorticoListeningHarness *self=me;(void)self;NSString *owner=self->_owner.activeOwner;
    [self->_owner action:@"pause" value:nil owner:owner];
    [self after:2 then:^{[me->_owner action:@"resume" value:nil owner:owner];
     [me wait:^BOOL{return [me->_last[@"session"][@"itemId"] isEqual:@"long-2"];} timeout:30 then:^(BOOL advanced){
      [me after:3 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"];
       NSMutableDictionary *r=[@{@"prepared":@(ok),@"advanced":@(advanced),@"commits":@([self delta:st key:@"commits"]),@"preparations":@([self delta:st key:@"preparations"]),@"long2Sessions":@(second.count),@"snapshots":self->_snapshots} mutableCopy];[r addEntriesFromDictionary:[self timelineChecks:st]];
       r[@"pass"]=@(ok&&advanced&&[self delta:st key:@"commits"]==0&&second.count==1&&[r[@"liveSessions"] integerValue]<=1);done(r);}];}];
     }];
    }];
   }];
  }];return;
 }
 if([name hasPrefix:@"fallback-"]||[name isEqual:@"commit-lost-once"]){
  // (e): a failed prepare or commit: the next track plays exactly once, through :advance.
  NSArray *parts=[name componentsSeparatedByString:@"-"];BOOL lost=[name isEqual:@"commit-lost-once"];
  NSString *match=lost?@"commit-next":[parts[1] isEqual:@"prepare"]?@"prepare-next":@"commit-next";id outcome=lost?@"offline":[parts[2] isEqual:@"offline"]?@"offline":@([parts[2] integerValue]);
  NSInteger count=lost?1:[parts[2] isEqual:@"offline"]&&[parts[1] isEqual:@"commit"]?6:1;
  NSMutableArray *outcomes=[NSMutableArray new];for(NSInteger k=0;k<count;k++)[outcomes addObject:outcome];
  [self request:@"POST" path:@"/__test/fail" body:@{@"match":match,@"outcomes":outcomes} headers:nil done:^(NSInteger s,id v){
   [me start:@[@"long-1",@"long-2"] effects:gapless seek:13 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
    [me wait:^BOOL{return [me->_last[@"session"][@"itemId"] isEqual:@"long-2"];} timeout:60 then:^(BOOL advanced){
     [me after:3 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"];NSInteger advances=[self newAdvances:st],commits=[self delta:st key:@"commits"];
      NSMutableDictionary *r=[@{@"advanced":@(advanced),@"commits":@(commits),@"preparations":@([self delta:st key:@"preparations"]),@"long2Sessions":@(second.count),@"advances":@(advances),@"boundaries":[self boundaries],@"snapshots":self->_snapshots} mutableCopy];
      [r addEntriesFromDictionary:[self timelineChecks:st]];
      BOOL once=advanced&&second.count==1&&[r[@"liveSessions"] integerValue]<=1;
      r[@"pass"]=@(lost?(once&&commits==1&&advances==0):(once&&commits==0&&advances==1));done(r);}];}];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"commit-answer-lost"]){
  // (INT T2) The commit lands but its answer is lost, and every retry fails: the owner keeps the
  // token, the old session reads as completed, and a replay adopts the committed session. Once.
  [self request:@"POST" path:@"/__test/fail" body:@{@"match":@"commit-next",@"outcomes":@[@"lost",@"offline",@"offline",@"offline",@"offline",@"offline"]} headers:nil done:^(NSInteger s,id v){
   [me start:@[@"long-1",@"long-2"] effects:gapless seek:13 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
    [me wait:^BOOL{return [me->_last[@"session"][@"itemId"] isEqual:@"long-2"]||[me->_last[@"terminal"] boolValue];} timeout:60 then:^(BOOL advanced){
     [me after:3 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"];
      NSInteger commits=[self delta:st key:@"commits"],advances=[self newAdvances:st];
      done(@{@"advanced":@(advanced),@"terminal":self->_last[@"terminal"]?:@NO,@"commits":@(commits),@"advances":@(advances),@"long2Sessions":@(second.count),@"snapshots":self->_snapshots,
       @"pass":@(advanced&&![self->_last[@"terminal"] boolValue]&&commits==1&&advances==0&&second.count==1)});}];}];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"skip-in-drain"]){
  // (INT T3) Next between commit and boundary plays the committed successor now; no :advance.
  [self start:@[@"long-1",@"long-2",@"long-3"] effects:@{@"gapless":@NO,@"crossfadeSeconds":@6,@"normalization":@"off"} seek:8 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   __block NSInteger commits=0;
   [me wait:^BOOL{if([[me->_facts valueForKey:@"type"] containsObject:@"audioCommit"])[me state:^(NSDictionary *st){commits=[me delta:st key:@"commits"];}];return commits>=1;} timeout:30 then:^(BOOL committed){
    [me->_owner advance:@"next" entry:nil owner:me->_owner.activeOwner];
    [me after:4 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"],*third=[self newSessions:st item:@"long-3"];
     BOOL playing=[self->_last[@"session"][@"itemId"] isEqual:@"long-2"]&&[[self->_controller listeningSnapshot][@"effectiveRate"] doubleValue]>0;
     done(@{@"committed":@(committed),@"nowPlaying":self->_last[@"session"][@"itemId"]?:@"",@"playing":@(playing),@"long2Sessions":@(second.count),@"long3Sessions":@(third.count),@"advances":@([self newAdvances:st]),@"snapshots":self->_snapshots,
      @"pass":@(committed&&playing&&second.count==1&&third.count==0&&[self newAdvances:st]==0)});}];}];
   }];
  }];return;
 }
 if([name isEqual:@"crossfade-rate"]){
  // Music is fixed at 1x. A rejected rate request cannot change the 6 s wall-time overlap.
  [self start:@[@"long-1",@"long-2"] effects:@{@"gapless":@NO,@"crossfadeSeconds":@6,@"normalization":@"off"} seek:4 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   BOOL refused=![me->_owner action:@"rate" value:@1.25 owner:me->_owner.activeOwner];
   [me wait:^BOOL{return [me boundaries].count>=1;} timeout:45 then:^(BOOL ok){
    [me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSDictionary *b=[self boundaries].firstObject;double overlap=[b[@"overlapSeconds"] doubleValue];
     done(@{@"boundary":b?:NSNull.null,@"overlapSeconds":@(overlap),@"overlapErrorMs":@((overlap-6)*1000),@"commits":@([self delta:st key:@"commits"]),@"musicRateRequestRefused":@(refused),@"pass":@(ok&&refused&&fabs(overlap-6)<=.05&&[self delta:st key:@"commits"]==1)});}];
   }];
  }];return;
 }
 if([name isEqual:@"route-change"]){
  // A route change mid-track: the engine stops and reconfigures; playback carries on from where it was.
  [self start:@[@"long-1",@"long-2"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me after:5 then:^{
    double before=[[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue];
    [me->_controller debugAudioConfigurationChange];
    [me after:3 then:^{
     double after=[[me->_controller listeningSnapshot][@"positionSeconds"] doubleValue];BOOL rolling=[[me->_controller listeningSnapshot][@"effectiveRate"] doubleValue]>0;
     [me wait:^BOOL{return [me->_last[@"session"][@"itemId"] isEqual:@"long-2"];} timeout:30 then:^(BOOL advanced){
      [me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"];
       // 3 s later the clock should be about 3 s on (minus a restart's rebuffering), never back at 0.
       double moved=after-before;
       done(@{@"positionBefore":@(before),@"positionAfter3s":@(after),@"rollingAfter":@(rolling),@"advanced":@(advanced),@"long2Sessions":@(second.count),@"snapshots":self->_snapshots,
        @"pass":@(rolling&&moved>1.5&&moved<3.5&&advanced&&second.count==1)});}];
     }];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"same-album"]||[name isEqual:@"album-skip"]){
  // Plexamp's rule (§18.5): with a 6 s crossfade set, consecutive tracks of one album still join
  // gaplessly on one node; tracks 1 and 3 of it (a skip, as in shuffle) crossfade.
  BOOL same=[name isEqual:@"same-album"];
  NSArray *items=same?@[@"fx-album-flac-01",@"fx-album-flac-02",@"fx-album-flac-03"]:@[@"fx-album-flac-01",@"fx-album-flac-03"];NSUInteger edges=items.count-1;
  [self start:items effects:@{@"gapless":@NO,@"crossfadeSeconds":@6,@"normalization":@"off"} seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [me boundaries].count>=edges;} timeout:60 then:^(BOOL ok){
    [me after:2 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *b=[self boundaries];
     BOOL expected=b.count>=edges;
     for(NSDictionary *f in b)expected=expected&&(same?([f[@"chained"] boolValue]&&[f[@"fadeSeconds"] doubleValue]==0&&[f[@"gapFrames"] longLongValue]==0):(![f[@"chained"] boolValue]&&[f[@"fadeSeconds"] doubleValue]>0));
     done(@{@"boundaries":b,@"commits":@([self delta:st key:@"commits"]),@"pass":@(ok&&expected&&[self delta:st key:@"commits"]==(NSInteger)edges)});}];}];
   }];
  }];return;
 }
 if([name hasPrefix:@"book-"]){[self bookScenario:name gapless:gapless done:done];return;}
 if([name isEqual:@"shared-cache-readers"]){
  // INT T6: a second reader of a cache key whose file is still partial (repeat one; the committed
  // deck opening the playing track) must not truncate the bytes the first reader counts as present.
  [self start:@[@"long-1"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   NSURL *url=[NSURL URLWithString:[NSString stringWithFormat:@"%@/v1/media/grant-%@/audio",me->_origin,session[@"id"]]];
   dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED,0),^{NSDictionary *r=[PorticoAudioRender debugSharedReaders:url];dispatch_async(dispatch_get_main_queue(),^{done(r);});});
  }];return;
 }
 if([name isEqual:@"route-change-edge"]){
  // A route change between commit and boundary: the committed successor still joins, exactly once.
  [self start:@[@"long-1",@"long-2"] effects:gapless seek:13 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me wait:^BOOL{return [[me->_facts valueForKey:@"type"] containsObject:@"audioCommit"];} timeout:30 then:^(BOOL committing){
    [me after:0.5 then:^{
     [me->_controller debugAudioConfigurationChange];
     [me wait:^BOOL{return [me->_last[@"session"][@"itemId"] isEqual:@"long-2"];} timeout:30 then:^(BOOL advanced){
      [me after:3 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"];
       done(@{@"committing":@(committing),@"advanced":@(advanced),@"commits":@([self delta:st key:@"commits"]),@"advances":@([self newAdvances:st]),@"long2Sessions":@(second.count),@"boundaries":[self boundaries],@"snapshots":self->_snapshots,
        @"pass":@(committing&&advanced&&second.count==1&&[self delta:st key:@"commits"]+[self newAdvances:st]==1)});}];}];
     }];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"terminate"]){
  // (i): an administrator ends the session: its message, terminal, nothing advances.
  [self start:@[@"long-1",@"long-2"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   [me after:3 then:^{
    [me request:@"POST" path:[NSString stringWithFormat:@"/v1/admin/sessions/%@:terminate",session[@"id"]] body:@{@"message":@"Maintenance"} headers:nil done:^(NSInteger s,id v){
     [me wait:^BOOL{return [me->_last[@"terminal"] boolValue];} timeout:20 then:^(BOOL terminal){
      [me after:3 then:^{[me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSArray *second=[self newSessions:st item:@"long-2"];NSDictionary *end=self->_last[@"end"];
       done(@{@"terminal":@(terminal),@"end":end?:NSNull.null,@"error":self->_last[@"error"]?:NSNull.null,@"long2Sessions":@(second.count),@"snapshots":self->_snapshots,@"pass":@(terminal&&[end[@"reason"] isEqual:@"terminated"]&&[end[@"message"] isEqual:@"Maintenance"]&&second.count==0)});}];}];
     }];
    }];
   }];
  }];return;
 }
 if([name isEqual:@"lease"]){
  // (i): no accepted report for 120 s pauses audio; the server's lease_expired then ends it.
  [self start:@[@"long-5",@"long-1"] effects:gapless seek:0 done:^(NSDictionary *session){if(!session){done(@{@"error":@"start failed"});return;}
   double began=NSProcessInfo.processInfo.systemUptime;
   [me after:2 then:^{
    [me request:@"POST" path:@"/__test/outage" body:@{@"match":@"/timeline",@"until":@((long long)(Wall()*1000)+135000)} headers:nil done:^(NSInteger s,id v){
     [me wait:^BOOL{return [me->_last[@"intent"] isEqual:@"paused"];} timeout:140 then:^(BOOL paused){
      double pausedAt=NSProcessInfo.processInfo.systemUptime-began;
      [me wait:^BOOL{return [me->_last[@"terminal"] boolValue];} timeout:40 then:^(BOOL terminal){
       [me state:^(NSDictionary *st){PorticoListeningHarness *self=me;(void)self;NSDictionary *end=self->_last[@"end"];
        done(@{@"pausedAfterSeconds":@(pausedAt),@"paused":@(paused),@"terminal":@(terminal),@"end":end?:NSNull.null,@"snapshots":self->_snapshots,@"long1Sessions":@([self newSessions:st item:@"long-1"].count),
         @"pass":@(paused&&pausedAt>=118&&pausedAt<=130&&terminal&&[end[@"reason"] isEqual:@"lease_expired"]&&[self newSessions:st item:@"long-1"].count==0)});}];
      }];
     }];
    }];
   }];
  }];return;
 }
 done(@{@"error":@"unknown scenario"});
}
@end
#endif
