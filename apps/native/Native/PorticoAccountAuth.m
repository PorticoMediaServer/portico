#import <React/RCTBridgeModule.h>
#import <AuthenticationServices/AuthenticationServices.h>
#import <Security/Security.h>
#import <UIKit/UIKit.h>
#import <TargetConditionals.h>
#import <objc/message.h>
#import "PorticoSecureStorageRead.h"

// Account continuations are not credentials. Only the existing credential owner
// stores Portico sessions; neither provider assertions nor passwords are persisted.
@interface PorticoAccountAuth : NSObject <RCTBridgeModule, ASAuthorizationControllerDelegate, ASAuthorizationControllerPresentationContextProviding
#if !TARGET_OS_TV
, ASWebAuthenticationPresentationContextProviding
#endif
>
@property(nonatomic,strong) ASAuthorizationController *apple;
#if !TARGET_OS_TV
@property(nonatomic,strong) ASWebAuthenticationSession *browser;
#endif
@property(nonatomic,strong) UIWindow *anchor;
@property(nonatomic,copy) NSString *appleState;
@property(nonatomic,copy) RCTPromiseResolveBlock resolve;
@property(nonatomic,copy) RCTPromiseRejectBlock reject;
@property(nonatomic,assign) NSUInteger generation;
@end
@implementation PorticoAccountAuth
RCT_EXPORT_MODULE(PorticoAccountAuth)
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (NSDictionary *)constantsToExport {
  return @{ @"bundleId": NSBundle.mainBundle.bundleIdentifier ?: @"", @"canSignInApple": @YES,
#if TARGET_OS_TV
    @"canOpenBrowser": @NO
#else
    @"canOpenBrowser": @YES
#endif
  };
}
- (NSMutableDictionary *)query:(NSString *)origin {
  NSURLComponents *url=[NSURLComponents componentsWithString:origin];
  BOOL loopback=[@[@"localhost",@"127.0.0.1",@"[::1]",@"::1"] containsObject:url.host];
  if (origin.length>2048 || !url.host.length || url.user.length || url.password.length || url.query.length || url.fragment.length || url.path.length ||
      !([url.scheme isEqualToString:@"https"] || ([url.scheme isEqualToString:@"http"] && loopback))) return nil;
  return [@{(__bridge id)kSecClass:(__bridge id)kSecClassGenericPassword,(__bridge id)kSecAttrService:@"tv.getportico.portico.account-continuation",
    (__bridge id)kSecAttrAccount:origin,(__bridge id)kSecAttrSynchronizable:@NO} mutableCopy];
}
RCT_REMAP_METHOD(newBinding, bindingResolve:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  unsigned char bytes[32]; if (SecRandomCopyBytes(kSecRandomDefault,sizeof(bytes),bytes)!=errSecSuccess) { reject(@"random",@"Secure randomness is unavailable.",nil);return; }
  NSString *s=[[NSData dataWithBytes:bytes length:sizeof(bytes)] base64EncodedStringWithOptions:0];
  resolve([[[s stringByReplacingOccurrencesOfString:@"+" withString:@"-"] stringByReplacingOccurrencesOfString:@"/" withString:@"_"] stringByReplacingOccurrencesOfString:@"=" withString:@""]);
}
RCT_REMAP_METHOD(newRequestId, requestResolve:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) { resolve(NSUUID.UUID.UUIDString); }
RCT_REMAP_METHOD(loadContinuation, loadOrigin:(NSString *)origin resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  NSMutableDictionary *q=[self query:origin]; if(!q){reject(@"origin",@"Invalid account origin.",nil);return;}
  q[(__bridge id)kSecReturnData]=@YES;q[(__bridge id)kSecMatchLimit]=(__bridge id)kSecMatchLimitOne;
  CFTypeRef result=NULL; OSStatus status=SecItemCopyMatching((__bridge CFDictionaryRef)q,&result);
  id data=CFBridgingRelease(result);
  PorticoResolveSecureStorageRead(status,data,NSNull.null,@"The private account continuation could not be read. Unlock this device and retry.",resolve,reject);
}
RCT_REMAP_METHOD(saveContinuation, saveOrigin:(NSString *)origin value:(NSString *)value resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  NSMutableDictionary *q=[self query:origin]; NSData *data=[value dataUsingEncoding:NSUTF8StringEncoding];
  if(!q || !data.length || data.length>8192){reject(@"continuation",@"Invalid account continuation.",nil);return;}
  NSDictionary *attributes=@{(__bridge id)kSecValueData:data,(__bridge id)kSecAttrAccessible:(__bridge id)kSecAttrAccessibleWhenUnlockedThisDeviceOnly};
  OSStatus status=SecItemUpdate((__bridge CFDictionaryRef)q,(__bridge CFDictionaryRef)attributes);
  if(status==errSecItemNotFound){[q addEntriesFromDictionary:attributes];status=SecItemAdd((__bridge CFDictionaryRef)q,NULL);}
  if(status!=errSecSuccess){reject(@"secure_storage",@"The account continuation could not be saved securely.",nil);return;} resolve(NSNull.null);
}
- (UIWindow *)activeWindow {
  for(UIScene *scene in UIApplication.sharedApplication.connectedScenes) {
    if(scene.activationState==UISceneActivationStateForegroundActive && [scene isKindOfClass:UIWindowScene.class]) {
      for(UIWindow *window in ((UIWindowScene *)scene).windows) if(window.isKeyWindow) return window;
    }
  }
  return nil;
}
- (ASPresentationAnchor)presentationAnchorForAuthorizationController:(ASAuthorizationController *)controller { return self.anchor; }
#if !TARGET_OS_TV
- (ASPresentationAnchor)presentationAnchorForWebAuthenticationSession:(ASWebAuthenticationSession *)session { return self.anchor; }
#endif
- (void)finish:(id)value code:(NSString *)code message:(NSString *)message {
  RCTPromiseResolveBlock resolve=self.resolve; RCTPromiseRejectBlock reject=self.reject;
  self.resolve=nil;self.reject=nil;self.apple.delegate=nil;self.apple=nil;self.appleState=nil;self.anchor=nil;
#if !TARGET_OS_TV
  self.browser=nil;
#endif
  if(code){if(reject)reject(code,message,nil);}else if(resolve)resolve(value);
}
- (BOOL)begin:(RCTPromiseResolveBlock)resolve reject:(RCTPromiseRejectBlock)reject {
  if(self.resolve){reject(@"busy",@"Finish or cancel the current provider sign-in first.",nil);return NO;}
  self.anchor=[self activeWindow]; if(!self.anchor){reject(@"foreground",@"Return to the app before signing in.",nil);return NO;}
  self.resolve=resolve;self.reject=reject;self.generation++;return YES;
}
RCT_REMAP_METHOD(openBrowser, browserURL:(NSString *)raw transactionId:(NSString *)expected resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
#if TARGET_OS_TV
  reject(@"browser_unavailable",@"Use browser activation on a phone or computer for Google sign-in.",nil);
#else
  NSURLComponents *url=[NSURLComponents componentsWithString:raw];
  NSCharacterSet *bad=[[NSCharacterSet characterSetWithCharactersInString:@"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"] invertedSet];
  if(![url.scheme isEqualToString:@"https"] || ![@[@"accounts.google.com",@"appleid.apple.com"] containsObject:url.host] || url.user.length || url.password.length || url.port || raw.length>16384 || !expected.length || expected.length>160 || [expected rangeOfCharacterFromSet:bad].location!=NSNotFound){reject(@"provider_url",@"The provider return is invalid.",nil);return;}
  if(![self begin:resolve reject:reject])return;
  NSUInteger generation=self.generation;__weak PorticoAccountAuth *weakSelf=self;
  self.browser=[[ASWebAuthenticationSession alloc] initWithURL:url.URL callbackURLScheme:@"portico" completionHandler:^(NSURL *callback,NSError *error){
    dispatch_async(dispatch_get_main_queue(),^{PorticoAccountAuth *self=weakSelf;if(!self || self.generation!=generation || !self.resolve)return;
      if(error || !callback){[self finish:nil code:@"provider_cancelled" message:@"Provider sign-in was cancelled or could not finish."];return;}
      NSURLComponents *r=[NSURLComponents componentsWithURL:callback resolvingAgainstBaseURL:NO];NSArray<NSURLQueryItem *> *items=r.queryItems;
      // SEC-01: an older Portico Account returns the transaction only; a current one adds its
      // completion code (continued here by the account-return page). The whole return goes to JS,
      // which checks it strictly (client-core parseAccountReturn); here only its outline.
      BOOL withCode=items.count==2 && [items[1].name isEqualToString:@"code"] && items[1].value.length==43 && [items[1].value rangeOfCharacterFromSet:bad].location==NSNotFound;
      if(![r.scheme isEqualToString:@"portico"] || ![r.host isEqualToString:@"account-return"] || r.path.length || r.user.length || r.password.length || r.port || r.fragment.length || !(items.count==1 || withCode) || ![items.firstObject.name isEqualToString:@"transactionId"] || ![items.firstObject.value isEqualToString:expected] || callback.absoluteString.length>1024){[self finish:nil code:@"provider_return" message:@"The provider returned a different sign-in attempt. Start again."];return;}
      [self finish:callback.absoluteString code:nil message:nil];
    });
  }];
  self.browser.presentationContextProvider=self;self.browser.prefersEphemeralWebBrowserSession=YES;
  if(![self.browser start])[self finish:nil code:@"provider_start" message:@"The provider browser could not start."];
#endif
}
RCT_REMAP_METHOD(signInApple, appleNonce:(NSString *)nonce resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  if(nonce.length<16 || nonce.length>256){reject(@"nonce",@"The Apple sign-in challenge is invalid.",nil);return;}
  if(![self begin:resolve reject:reject])return;
  ASAuthorizationAppleIDRequest *request=[ASAuthorizationAppleIDProvider.new createRequest];
  request.requestedScopes=@[ASAuthorizationScopeFullName,ASAuthorizationScopeEmail];request.nonce=nonce;
  self.appleState=NSUUID.UUID.UUIDString;request.state=self.appleState;
  self.apple=[[ASAuthorizationController alloc] initWithAuthorizationRequests:@[request]];
  self.apple.delegate=self;self.apple.presentationContextProvider=self;[self.apple performRequests];
}
- (void)authorizationController:(ASAuthorizationController *)controller didCompleteWithAuthorization:(ASAuthorization *)authorization {
  if(controller!=self.apple || !self.resolve)return;
  if(![authorization.credential isKindOfClass:ASAuthorizationAppleIDCredential.class]){[self finish:nil code:@"apple_credential" message:@"Apple did not return an identity assertion."];return;}
  ASAuthorizationAppleIDCredential *credential=(ASAuthorizationAppleIDCredential *)authorization.credential;
  NSString *token=credential.identityToken.length ? [[NSString alloc] initWithData:credential.identityToken encoding:NSUTF8StringEncoding] : nil;
  if(![credential.state isEqualToString:self.appleState] || !token.length || token.length>16384){[self finish:nil code:@"apple_credential" message:@"Apple returned an invalid sign-in response."];return;}
  NSString *name=credential.fullName?[NSPersonNameComponentsFormatter localizedStringFromPersonNameComponents:credential.fullName style:NSPersonNameComponentsFormatterStyleDefault options:0]:@"";
  [self finish:@{@"idToken":token,@"displayName":name ?: @""} code:nil message:nil];
}
- (void)authorizationController:(ASAuthorizationController *)controller didCompleteWithError:(NSError *)error {
  if(controller==self.apple)[self finish:nil code:@"provider_cancelled" message:@"Apple sign-in was cancelled or could not finish."];
}
RCT_REMAP_METHOD(cancel, cancelResolve:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  self.generation++;
#if !TARGET_OS_TV
  [self.browser cancel];
#endif
  // cancel is a public API on newer SDKs. Older supported systems still fence
  // the delegate result immediately; their system sheet can be dismissed normally.
  SEL selector=NSSelectorFromString(@"cancel");
  if([self.apple respondsToSelector:selector])((void(*)(id,SEL))objc_msgSend)(self.apple,selector);
  [self finish:nil code:@"provider_cancelled" message:@"Provider sign-in was cancelled."];resolve(NSNull.null);
}
@end
