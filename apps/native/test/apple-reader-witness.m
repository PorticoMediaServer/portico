// Diagnostic UIKit host for one collected final Read build graph. Not an app
// route, session, reading facade, or production resource entry.
#import <UIKit/UIKit.h>
#import <WebKit/WebKit.h>
#import "../Native/PorticoReaderAssetOrigin.h"

@interface ReaderWitnessScene : UIResponder <UIWindowSceneDelegate,WKNavigationDelegate>
@property(nonatomic,strong) UIWindow *window;
@property(nonatomic,strong) WKWebView *webView;
@property(nonatomic) BOOL finished;
@end
@implementation ReaderWitnessScene
- (void)finish:(NSDictionary *)proof cleanup:(id)cleanup{
 if(_finished)return;_finished=YES;
 NSDictionary *result=@{@"proof":proof?:@{},@"cleanup":cleanup?:NSNull.null,@"native":@{@"url":_webView.URL.absoluteString?:@"",@"systemVersion":UIDevice.currentDevice.systemVersion}};
 NSData *data=[NSJSONSerialization dataWithJSONObject:result options:NSJSONWritingPrettyPrinted error:NULL];
 NSURL *file=[[NSFileManager.defaultManager URLsForDirectory:NSDocumentDirectory inDomains:NSUserDomainMask].firstObject URLByAppendingPathComponent:@"reader-witness-result.json"];
 [data writeToURL:file atomically:YES];
 printf("READER_WITNESS_RESULT %s\n",[[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] UTF8String]);fflush(stdout);
 [_webView stopLoading];_webView.navigationDelegate=nil;[_webView removeFromSuperview];_webView=nil;
}
- (void)scene:(UIScene *)scene willConnectToSession:(UISceneSession *)session options:(UISceneConnectionOptions *)options{
 if(![scene isKindOfClass:UIWindowScene.class])return;
 WKWebViewConfiguration *configuration=[WKWebViewConfiguration new];configuration.websiteDataStore=WKWebsiteDataStore.nonPersistentDataStore;
 PorticoConfigureReaderWebView(configuration);
 UIViewController *controller=[UIViewController new];_window=[[UIWindow alloc] initWithWindowScene:(UIWindowScene *)scene];_window.rootViewController=controller;[_window makeKeyAndVisible];
 _webView=[[WKWebView alloc] initWithFrame:controller.view.bounds configuration:configuration];_webView.autoresizingMask=UIViewAutoresizingFlexibleWidth|UIViewAutoresizingFlexibleHeight;_webView.navigationDelegate=self;[controller.view addSubview:_webView];
 NSURL *entry=PorticoReaderEntryURL();
 if(entry)[_webView loadRequest:[NSURLRequest requestWithURL:entry]];
 else [self finish:@{@"status":@"fail",@"stage":@"native_bundle_unavailable"} cleanup:nil];
 __weak ReaderWitnessScene *weakSelf=self;
 dispatch_after(dispatch_time(DISPATCH_TIME_NOW,125*NSEC_PER_SEC),dispatch_get_main_queue(),^{[weakSelf finish:@{@"status":@"fail",@"stage":@"native_witness_timeout"} cleanup:nil];});
}
- (void)sceneDidDisconnect:(UIScene *)scene{[self finish:@{@"status":@"fail",@"stage":@"native_scene_disconnected"} cleanup:nil];}
- (void)webView:(WKWebView *)webView decidePolicyForNavigationAction:(WKNavigationAction *)action decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler{
 decisionHandler([action.request.URL.absoluteString isEqual:PorticoReaderOrigin]?WKNavigationActionPolicyAllow:WKNavigationActionPolicyCancel);
}
- (void)webView:(WKWebView *)webView didFinishNavigation:(WKNavigation *)navigation{
 [webView callAsyncJavaScript:@"if (!window.snapshotProof) return {status:'fail',stage:'bootstrap_missing',errors:window.readerFloorErrors||[]}; return await window.snapshotProof;" arguments:@{} inFrame:nil inContentWorld:WKContentWorld.pageWorld completionHandler:^(id proof,NSError *error){
  if(self.finished)return;
  if(error){[self finish:@{@"status":@"fail",@"stage":@"native_proof_evaluation",@"error":error.description} cleanup:nil];return;}
  [webView callAsyncJavaScript:@"return window.readerCleanup || null;" arguments:@{} inFrame:nil inContentWorld:WKContentWorld.pageWorld completionHandler:^(id cleanup,NSError *cleanupError){
   [self finish:[proof isKindOfClass:NSDictionary.class]?proof:@{@"status":@"fail",@"stage":@"invalid_proof_result"} cleanup:cleanupError?@{@"error":cleanupError.description}:cleanup];
  }];
 }];
}
- (void)navigationFailed:(NSError *)error{[self finish:@{@"status":@"fail",@"stage":@"native_navigation",@"error":error.description} cleanup:nil];}
- (void)webView:(WKWebView *)webView didFailNavigation:(WKNavigation *)navigation withError:(NSError *)error{[self navigationFailed:error];}
- (void)webView:(WKWebView *)webView didFailProvisionalNavigation:(WKNavigation *)navigation withError:(NSError *)error{[self navigationFailed:error];}
@end
@interface ReaderWitnessDelegate : UIResponder <UIApplicationDelegate> @end
@implementation ReaderWitnessDelegate
- (UISceneConfiguration *)application:(UIApplication *)application configurationForConnectingSceneSession:(UISceneSession *)session options:(UISceneConnectionOptions *)options{
 UISceneConfiguration *configuration=[UISceneConfiguration configurationWithName:@"ReaderWitness" sessionRole:session.role];
 configuration.delegateClass=ReaderWitnessScene.class;return configuration;
}
@end
int main(int argc,char **argv){@autoreleasepool{return UIApplicationMain(argc,argv,nil,NSStringFromClass(ReaderWitnessDelegate.class));}}
