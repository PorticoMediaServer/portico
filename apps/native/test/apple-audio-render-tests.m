#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import <objc/runtime.h>
#if PORTICO_UIKIT_TEST_APP
#import <UIKit/UIKit.h>
#endif
#import "../Native/PorticoAudioRender.h"
#import "../Native/PorticoAudioProcessing.h"
#import "../Native/PorticoServerTrust.h"

// Local authored fixtures exercise the real decoder, nodes, clocks and local store. No
// network or trust behavior is simulated by this test (the TLS test covers that separately).
@implementation PorticoServerTrust
+ (void)handleChallenge:(NSURLAuthenticationChallenge *)challenge completion:(void (^)(NSURLSessionAuthChallengeDisposition,NSURLCredential *))completion {completion(NSURLSessionAuthChallengeCancelAuthenticationChallenge,nil);}
@end
static NSUInteger rateRequests;
@interface AVAudioSession (PorticoRateTest)
- (BOOL)portico_test_setPreferredSampleRate:(double)rate error:(NSError **)error;
@end
@implementation AVAudioSession (PorticoRateTest)
- (BOOL)portico_test_setPreferredSampleRate:(double)rate error:(NSError **)error {rateRequests++;return [self portico_test_setPreferredSampleRate:rate error:error];}
@end
static BOOL expect(BOOL value,const char *what){if(!value)fprintf(stderr,"FAIL %s\n",what);return value;}
static BOOL waitFor(BOOL (^ready)(void)){
 NSDate *until=[NSDate dateWithTimeIntervalSinceNow:12];
 while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];
 return ready();
}
static NSDictionary *plan(NSString *path,NSString *identity,BOOL gain){
 NSMutableDictionary *p=[@{@"version":@2,@"mode":@"direct",@"id":identity,@"container":@"wav",@"codec":@"pcm_s16le",@"url":[NSURL fileURLWithPath:path].absoluteString,@"sampleRate":@8000,@"channels":@2,@"durationFrames":@16000,@"trim":@{@"startFrames":@0,@"endFrames":@0}}mutableCopy];
 if(gain)p[@"gain"]=@{@"trackDb":@(-6),@"trackPeak":@.1};return p;
}
static NSDictionary *command(NSDictionary *p,NSDictionary *prepared){
 NSMutableDictionary *r=[@{@"plan":p,@"settings":@{@"gapless":@YES,@"crossfadeSeconds":@0,@"normalization":@"track"},@"epoch":@0,@"startSeconds":@0}mutableCopy];
 if(prepared){r[@"prepared"]=prepared;r[@"committed"]=prepared;}
 return @{@"audioRender":r,@"rate":@1,@"paused":@NO};
}
static int runTests(int argc,const char **argv){@autoreleasepool{
 if(argc!=2||![NSProcessInfo.processInfo.environment[@"PORTICO_APPLE_ISOLATED_SIMULATOR_TESTS"] isEqual:@"1"])return 2;
 if([NSProcessInfo.processInfo.environment[@"PORTICO_AUDIO_PLAIN_PROBE"]isEqual:@"1"]){
  // Isolate Core Audio/RemoteIO from all Portico store, decoder, DSP and transition code.
  AVAudioSession *session=AVAudioSession.sharedInstance;NSError *error=nil;
  BOOL configured=[session setCategory:AVAudioSessionCategoryPlayback error:&error]&&[session setPreferredSampleRate:48000 error:&error]&&[session setActive:YES error:&error];
  NSMutableArray *ports=[NSMutableArray new];for(AVAudioSessionPortDescription *port in session.currentRoute.outputs)[ports addObject:@{@"type":port.portType,@"channels":@(port.channels.count)}];
  printf("plain Core Audio configured=%d rate=%.0f channels=%lu route=%s\n",configured,session.sampleRate,(unsigned long)session.outputNumberOfChannels,[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:ports options:0 error:nil]encoding:NSUTF8StringEncoding].UTF8String);
  AVAudioEngine *engine=[AVAudioEngine new];AVAudioPlayerNode *node=[AVAudioPlayerNode new];[engine attachNode:node];AVAudioFormat *format=[[AVAudioFormat alloc]initStandardFormatWithSampleRate:48000 channels:2];[engine connect:node to:engine.mainMixerNode format:format];
  AVAudioPCMBuffer *buffer=[[AVAudioPCMBuffer alloc]initWithPCMFormat:format frameCapacity:48000];buffer.frameLength=48000;for(int c=0;c<2;c++)for(int i=0;i<48000;i++)buffer.floatChannelData[c][i]=(float)(.01*sin(2*M_PI*200*i/48000));[node scheduleBuffer:buffer completionHandler:nil];
  BOOL started=[engine startAndReturnError:&error];if(started)[node play];
  BOOL rendered=started&&waitFor(^BOOL{AVAudioTime *last=node.lastRenderTime;if(!last||!last.isSampleTimeValid)return NO;return [node playerTimeForNodeTime:last].sampleTime>480;});
  printf("{\"plainCoreAudioLiveClockPass\":%s,\"error\":%ld}\n",rendered?"true":"false",(long)error.code);[engine stop];return configured&&rendered?0:1;
 }
 method_exchangeImplementations(class_getInstanceMethod(AVAudioSession.class,@selector(setPreferredSampleRate:error:)),class_getInstanceMethod(AVAudioSession.class,@selector(portico_test_setPreferredSampleRate:error:)));
 NSString *file=[NSString stringWithUTF8String:argv[1]];PorticoAudioProcessing *store=PorticoAudioProcessing.shared;NSError *error=nil;BOOL ok=YES;
 NSDictionary *flat=@{@"enabled":@NO,@"preampDb":@0,@"bands":@[@0,@0,@0,@0,@0,@0,@0,@0,@0,@0]};
 ok=expect([store setEqualizer:flat error:&error]&&[store setSampleRateMatching:@"off" error:&error],"configure isolated local store")&&ok;
 PorticoAudioRender *render=[PorticoAudioRender new];NSMutableArray *facts=[NSMutableArray new];render.onFact=^(NSDictionary *f){[facts addObject:f];if([@[@"audioRenderFailed",@"ready",@"ended"]containsObject:f[@"type"]])printf("renderer fact %s\n",[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:f options:0 error:nil] encoding:NSUTF8StringEncoding].UTF8String);};
 [render applyCommand:command(plan(file,@"gain-song",YES),nil)];
 ok=expect(waitFor(^BOOL{return render.position>.1;}),"real player clock advanced")&&ok;
 if(!ok){printf("initial renderer snapshot %s\n",[[NSString alloc]initWithData:[NSJSONSerialization dataWithJSONObject:[render snapshot] options:0 error:nil] encoding:NSUTF8StringEncoding].UTF8String);[render invalidate];return 1;}
 NSDictionary *heard=[render snapshot];
 ok=expect(![heard[@"eqApplied"]boolValue]&&[heard[@"levelingApplied"]boolValue],"current rendered gain, bypassed EQ")&&ok;
 NSMutableDictionary *on=[flat mutableCopy];on[@"enabled"]=@YES;on[@"preampDb"]=@(-.5);ok=expect([store setEqualizer:on error:&error],"enable actual current graph")&&ok;
 ok=expect([[render snapshot][@"eqApplied"]boolValue],"actual connected EQ reports applied")&&ok;
 [render applyCommand:command(plan(file,@"no-gain-song",NO),nil)];
 NSDictionary *before=[render snapshot];
 ok=expect(![before[@"eqApplied"]boolValue]&&![before[@"levelingApplied"]boolValue],"new song never inherits processing observations")&&ok;
 ok=expect(waitFor(^BOOL{return render.position>.1;}),"next song rendered")&&ok;
 NSDictionary *noGain=[render snapshot];
 ok=expect([noGain[@"eqApplied"]boolValue]&&![noGain[@"levelingApplied"]boolValue],"normalization intent without metadata is not applied")&&ok;
 ok=expect(waitFor(^BOOL{return [[facts valueForKey:@"type"]containsObject:@"ended"];}),"actual end observed")&&ok;
 ok=expect(![[render snapshot][@"eqApplied"]boolValue]&&![[render snapshot][@"levelingApplied"]boolValue],"ended clears current processing facts")&&ok;
 [render invalidate];
 ok=expect(![[render snapshot][@"eqApplied"]boolValue]&&![[render snapshot][@"levelingApplied"]boolValue],"stop clears processing facts")&&ok;
 ok=expect([store setSampleRateMatching:@"strict" error:&error],"commit local Strict")&&ok;
 [facts removeAllObjects];NSUInteger start=rateRequests;
 NSDictionary *next=plan(file,@"strict-second",NO),*prepared=@{@"token":@"strict-next",@"plan":next};
 [render applyCommand:command(plan(file,@"strict-first",NO),prepared)];
 ok=expect(waitFor(^BOOL{return [[facts valueForKey:@"type"]containsObject:@"ended"];}),"Strict ends old deck for ordinary committed promotion")&&ok;
 ok=expect(![[facts valueForKey:@"type"]containsObject:@"audioBoundary"],"Strict never chains across session switch")&&ok;
 [render applyCommand:command(next,nil)];
 ok=expect(waitFor(^BOOL{return render.position>.1;}),"Strict successor rendered")&&ok;
 NSUInteger strictRequests=rateRequests-start;
 ok=expect(strictRequests>=2,"Strict re-requests equal rate for each song")&&ok;
 printf("{\"renderedProcessingFactsAndStrictPass\":%s,\"sameRateStrictOSRequests\":%lu,\"outputSampleRate\":%.0f}\n",ok?"true":"false",(unsigned long)strictRequests,AVAudioSession.sharedInstance.sampleRate);
 [render invalidate];return ok?0:1;
}}
#if PORTICO_UIKIT_TEST_APP
@interface PorticoAudioTestScene : UIResponder<UIWindowSceneDelegate>
@property(nonatomic,strong) UIWindow *window;
@end
@implementation PorticoAudioTestScene
- (void)scene:(UIScene *)scene willConnectToSession:(UISceneSession *)session options:(UISceneConnectionOptions *)options {
 self.window=[[UIWindow alloc]initWithWindowScene:(UIWindowScene *)scene];self.window.rootViewController=[UIViewController new];[self.window makeKeyAndVisible];
 // A main-queue block cannot drain nested main-queue decoder completions while it waits.
 // Start from a run-loop timer instead; production remains asynchronous on the main queue.
 [NSTimer scheduledTimerWithTimeInterval:1 repeats:NO block:^(NSTimer *timer){
  printf("Audio renderer UIKit harness started; isolated=%s\n",NSProcessInfo.processInfo.environment[@"PORTICO_APPLE_ISOLATED_SIMULATOR_TESTS"].UTF8String?:"absent");
  const char *args[]={"apple-audio-render-tests",[NSBundle.mainBundle pathForResource:@"render-test" ofType:@"wav"].UTF8String};
  int result=runTests(2,args);
  NSURL *file=[[NSFileManager.defaultManager URLsForDirectory:NSDocumentDirectory inDomains:NSUserDomainMask].firstObject URLByAppendingPathComponent:@"result.json"];
  [[NSJSONSerialization dataWithJSONObject:@{@"exit":@(result)} options:0 error:nil] writeToURL:file atomically:YES];exit(result);
 }];
}
@end
@interface PorticoAudioTestApp : UIResponder<UIApplicationDelegate> @end
@implementation PorticoAudioTestApp
- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)options {return YES;}
@end
int main(int argc,char **argv){@autoreleasepool{setvbuf(stdout,NULL,_IONBF,0);return UIApplicationMain(argc,argv,nil,NSStringFromClass(PorticoAudioTestApp.class));}}
#else
int main(int argc,const char **argv){return runTests(argc,argv);}
#endif
