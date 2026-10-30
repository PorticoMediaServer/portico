#import <UIKit/UIKit.h>

// Observe completion of the real worker AND every main-queue block it enqueues.
// Enter before enqueue and leave after the original block: the group cannot drain
// in the gap between the file read returning and its main completion running.
static dispatch_group_t observedSubtitleWork;
static _Thread_local BOOL insideObservedSubtitleWork;
static void PorticoTestDispatchAsync(dispatch_queue_t queue,dispatch_block_t work) {
 dispatch_group_t group=observedSubtitleWork;
 if(!group){dispatch_async(queue,work);return;}
 dispatch_group_enter(group);
 dispatch_async(queue,^{
  BOOL previous=insideObservedSubtitleWork;insideObservedSubtitleWork=YES;
  @try {work();} @finally {insideObservedSubtitleWork=previous;dispatch_group_leave(group);}
 });
}
#define dispatch_async PorticoTestDispatchAsync
#import "../Native/PorticoSubtitleFiles.m"
#undef dispatch_async

// Real React registration and presenter lookup are linked from the installed Pods.
static UIViewController *testPresenter;
static NSMutableArray<NSString *> *passed;
static int failures;
static BOOL check(BOOL condition,NSString *name) {if(condition){[passed addObject:name];return YES;}failures++;fprintf(stderr,"FAIL %s\n",name.UTF8String);return NO;}
static BOOL waitFor(BOOL (^ready)(void)) {NSDate *until=[NSDate dateWithTimeIntervalSinceNow:4];while(!ready()&&until.timeIntervalSinceNow>0)[NSRunLoop.currentRunLoop runUntilDate:[NSDate dateWithTimeIntervalSinceNow:.01]];return ready();}
static BOOL cancel(PorticoSubtitleFiles *subject,NSString *identifier) {__block BOOL result=NO;[subject cancel:identifier resolver:^(id value){result=[value boolValue];} rejecter:^(NSString *code,NSString *message,NSError *error){}];return result;}

#if !TARGET_OS_TV
@interface SlowSubtitlePicker : PorticoSubtitleFiles
@property(nonatomic,strong) dispatch_semaphore_t entered;
@property(nonatomic,strong) dispatch_semaphore_t resumeRead;
@property(nonatomic) BOOL observedWorker;
@property(nonatomic) BOOL resumed;
@end
@implementation SlowSubtitlePicker
- (NSData *)readSubtitle:(NSURL *)url limit:(NSUInteger)limit progress:(NSProgress *)progress error:(NSError **)error {
 self.observedWorker=insideObservedSubtitleWork;
 dispatch_semaphore_signal(self.entered);self.resumed=dispatch_semaphore_wait(self.resumeRead,dispatch_time(DISPATCH_TIME_NOW,5*NSEC_PER_SEC))==0;
 return [super readSubtitle:url limit:limit progress:progress error:error];
}
@end
static UIDocumentPickerViewController *picker(PorticoSubtitleFiles *subject) {return ((PorticoSubtitlePickRequest *)[subject valueForKey:@"request"]).picker;}
static void settledPresentation(void) {check(waitFor(^BOOL{return testPresenter.presentedViewController==nil;}),@"picker dismissal completed within bound");}
#endif

static int tests(void) {@autoreleasepool {
 passed=[NSMutableArray new];
 PorticoSubtitleFiles *subject=[PorticoSubtitleFiles new];
 __block NSInteger resolves=0,rejects=0;__block NSString *code=nil;__block NSDictionary *result=nil;
 RCTPromiseResolveBlock resolve=^(id value){resolves++;result=value;};
 RCTPromiseRejectBlock reject=^(NSString *value,NSString *message,NSError *error){rejects++;code=value;};
 [subject pick:@"../invalid" resolver:resolve rejecter:reject];check([code isEqual:@"subtitle_request_invalid"]&&resolves==0,@"invalid request identity refused");
 check(!cancel(subject,@"unknown"),@"unknown cancel does not claim a request");
#if TARGET_OS_TV
 code=nil;[subject pick:@"tv-request" resolver:resolve rejecter:reject];check([code isEqual:@"subtitle_picker_unavailable"]&&resolves==0,@"TV picker remains unavailable");
#else
 resolves=0;rejects=0;code=nil;
 [subject pick:@"request-A" resolver:resolve rejecter:reject];UIDocumentPickerViewController *old=picker(subject);
 check(waitFor(^BOOL{return old.presentingViewController!=nil;}),@"actual UIKit picker presented");
 check(!cancel(subject,@"request-other")&&rejects==0&&picker(subject)==old,@"wrong request cannot dismiss active picker");
 __block NSString *busy=nil;[subject pick:@"request-B" resolver:^(id value){} rejecter:^(NSString *value,NSString *message,NSError *error){busy=value;}];
 check([busy isEqual:@"subtitle_picker_busy"]&&picker(subject)==old,@"second pick cannot replace active owner");
 check(cancel(subject,@"request-A")&&rejects==1&&[code isEqual:@"cancelled"]&&resolves==0,@"matching cancel rejects exactly its own promise");settledPresentation();
 resolves=0;rejects=0;code=nil;[subject pick:@"request-B" resolver:resolve rejecter:reject];UIDocumentPickerViewController *current=picker(subject);
 [subject documentPickerWasCancelled:old];
 check(picker(subject)==current&&resolves==0&&rejects==0,@"stale picker callback cannot cancel a later request");
 NSURL *documents=[NSFileManager.defaultManager URLsForDirectory:NSDocumentDirectory inDomains:NSUserDomainMask].firstObject;
 NSURL *file=[documents URLByAppendingPathComponent:@"selected.srt"];
 NSData *content=[@"1\n00:00:00,000 --> 00:00:01,000\nSubtitle\n" dataUsingEncoding:NSUTF8StringEncoding];[content writeToURL:file atomically:YES];
 [subject documentPicker:current didPickDocumentsAtURLs:@[file]];
 check(waitFor(^BOOL{return resolves+rejects>0;})&&resolves==1&&rejects==0&&[result[@"format"]isEqual:@"srt"]&&[result[@"title"]isEqual:@"selected.srt"]&&[[NSData dataWithContentsOfURL:[NSURL URLWithString:result[@"fileUri"]]]isEqual:content]&&[result[@"fileUri"]hasPrefix:@"file://"]&&![result[@"fileUri"]isEqual:file.absoluteString]&&!result[@"data"],@"real coordinated file resolves a private staged copy");settledPresentation();
 check(!cancel(subject,@"request-B"),@"settled request cannot be cancelled again");
 __block BOOL discarded=NO;
 [subject discard:@[file.absoluteString] resolver:^(id value){discarded=[value boolValue];} rejecter:reject];
 check(!discarded&&[NSFileManager.defaultManager fileExistsAtPath:file.path],@"discard refuses original user file");
 NSURL *staged=[NSURL URLWithString:result[@"fileUri"]];
 [subject discard:@[result[@"fileUri"]] resolver:^(id value){discarded=[value boolValue];} rejecter:reject];
 check(discarded&&![NSFileManager.defaultManager fileExistsAtPath:staged.path],@"discard removes only staged file");
 NSProgress *progress=[NSProgress progressWithTotalUnitCount:1];NSError *error=nil;
 check([subject readSubtitle:file limit:content.length progress:progress error:&error].length==content.length,@"bounded reader accepts exact byte limit");
 check([subject readSubtitle:file limit:content.length-1 progress:progress error:&error]==nil,@"bounded reader refuses one byte over limit");
 [progress cancel];check([subject readSubtitle:file limit:content.length progress:progress error:&error]==nil,@"cancelled read returns no bytes");
 NSURL *idx=[documents URLByAppendingPathComponent:@"selected.idx"];[@"# VobSub index file, v7\nsize: 720x480\n" writeToURL:idx atomically:YES encoding:NSUTF8StringEncoding error:nil];
 resolves=0;rejects=0;code=nil;[subject pick:@"request-pair" resolver:resolve rejecter:reject];
 [subject documentPicker:picker(subject) didPickDocumentsAtURLs:@[idx]];
 check([code isEqual:@"subtitle_format"]&&resolves==0&&rejects==1,@"VobSub without selected companion remains refused");settledPresentation();
 SlowSubtitlePicker *slow=[SlowSubtitlePicker new];slow.entered=dispatch_semaphore_create(0);slow.resumeRead=dispatch_semaphore_create(0);
 __block NSInteger oldSettled=0,newSettled=0;__block NSString *oldCode=nil;
 observedSubtitleWork=dispatch_group_create();
 [slow pick:@"slow-old" resolver:^(id value){oldSettled++;} rejecter:^(NSString *value,NSString *message,NSError *error){oldSettled++;oldCode=value;}];
 [slow documentPicker:picker(slow) didPickDocumentsAtURLs:@[file]];
 check(dispatch_semaphore_wait(slow.entered,dispatch_time(DISPATCH_TIME_NOW,2*NSEC_PER_SEC))==0&&slow.observedWorker,@"production async read reached observed worker boundary");
 check(cancel(slow,@"slow-old")&&oldSettled==1&&[oldCode isEqual:@"cancelled"],@"cancel while reading settles only old promise");settledPresentation();
 [slow pick:@"slow-new" resolver:^(id value){newSettled++;} rejecter:^(NSString *value,NSString *message,NSError *error){newSettled++;}];UIDocumentPickerViewController *newPicker=picker(slow);
 dispatch_semaphore_signal(slow.resumeRead);
 __block BOOL oldWorkCompleted=NO;
 dispatch_group_notify(observedSubtitleWork,dispatch_get_main_queue(),^{oldWorkCompleted=YES;});
 check(waitFor(^BOOL{return oldWorkCompleted;})&&slow.resumed,@"old worker and its actual main completion finished before successor assertion");
 // Keep the observation group alive if a timeout left work outstanding.
 if(oldWorkCompleted)observedSubtitleWork=nil;
 check(picker(slow)==newPicker&&newSettled==0&&oldSettled==1,@"late cancelled read cannot settle or clear newer picker");
 [slow invalidate];check(newSettled==1&&picker(slow)==nil,@"bridge invalidation cancels owned picker");settledPresentation();
 resolves=0;rejects=0;code=nil;[subject pick:@"invalidate-active" resolver:resolve rejecter:reject];UIDocumentPickerViewController *invalidatedPicker=picker(subject);[subject invalidate];
 check(rejects==1&&[code isEqual:@"cancelled"]&&resolves==0,@"invalidation cancels pending promise exactly once");
 [subject documentPickerWasCancelled:invalidatedPicker];check(rejects==1,@"late invalidated callback is discarded");settledPresentation();
#endif
 [subject invalidate];code=nil;[subject pick:@"after-close" resolver:resolve rejecter:reject];check([code isEqual:@"bridge_closed"],@"invalidated bridge refuses new request");
 NSDictionary *receipt=@{@"exit":@(failures?1:0),@"passed":passed,@"assertions":@(passed.count+failures),@"failures":@(failures),@"scope":@"actual UIKit native boundary; no JS upload or genuine watch UI acceptance"};
 NSURL *dir=[NSFileManager.defaultManager URLsForDirectory:NSDocumentDirectory inDomains:NSUserDomainMask].firstObject;[[NSJSONSerialization dataWithJSONObject:receipt options:NSJSONWritingPrettyPrinted error:nil]writeToURL:[dir URLByAppendingPathComponent:@"result.json"] atomically:YES];
 fprintf(stderr,"%s\n",receipt.description.UTF8String);return failures?1:0;
}}
@interface PorticoSubtitlePickerTestScene : UIResponder<UIWindowSceneDelegate>
@property(nonatomic,strong) UIWindow *window;
@end
@implementation PorticoSubtitlePickerTestScene
- (void)scene:(UIScene *)scene willConnectToSession:(UISceneSession *)session options:(UISceneConnectionOptions *)options {
 self.window=[[UIWindow alloc]initWithWindowScene:(UIWindowScene *)scene];testPresenter=[UIViewController new];testPresenter.view.backgroundColor=UIColor.blackColor;self.window.rootViewController=testPresenter;[self.window makeKeyAndVisible];
 // A run-loop callback lets the suite's bounded nested run-loop waits service
 // UIKit and the production reader's dispatch-to-main completion. Running the
 // whole suite inside a main-queue block would starve those same callbacks.
 [self performSelector:@selector(runSuite) withObject:nil afterDelay:1];
}
- (void)runSuite {exit(tests());}
@end
@interface PorticoSubtitlePickerTestApp : UIResponder<UIApplicationDelegate> @end
@implementation PorticoSubtitlePickerTestApp
- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)options {return YES;}
@end
int main(int argc,char **argv) {@autoreleasepool{return UIApplicationMain(argc,argv,nil,NSStringFromClass(PorticoSubtitlePickerTestApp.class));}}
