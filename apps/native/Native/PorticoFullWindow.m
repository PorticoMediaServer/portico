#import "PorticoFullWindow.h"
#import <React/RCTBridgeModule.h>
#import <React/RCTEventEmitter.h>

#if TARGET_OS_TV

/** No television turns. The module stays compiled so the one JS contract is unchanged. */
@interface PorticoFullWindow : RCTEventEmitter <RCTBridgeModule> @end
@implementation PorticoFullWindow
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return NO; }
- (NSDictionary *)constantsToExport { return @{@"orientationSupported": @NO}; }
- (NSArray<NSString *> *)supportedEvents { return @[@"portico.fullwindow"]; }
RCT_EXPORT_METHOD(enter:(NSString *)owner resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {resolve(@NO);}
RCT_EXPORT_METHOD(leave:(NSString *)owner) {}
@end

#else

@interface PorticoFullWindow : RCTEventEmitter <RCTBridgeModule> @end

@interface PorticoFullWindow ()
/** Set inside the invalidating turn; every later turn for this instance refuses. */
@property (nonatomic, assign) BOOL porticoRetired;
@end

/** Adopted on the main thread when the app creates the window React renders into. */
static __weak UIWindow *PorticoFullWindowHostWindow;

/** The one request. Every field below is read and written only inside a main-queue turn. */
static __weak PorticoFullWindow *PorticoFullWindowOwnerModule;
static NSString *PorticoFullWindowOwner;
static __weak UIWindow *PorticoFullWindowWindow;
static __weak UIWindowScene *PorticoFullWindowScene;
static UIInterfaceOrientationMask PorticoFullWindowRestoreMask;

/** Owners retired before their posted turn ran. The only state touched across threads. */
static NSMutableSet<NSString *> *PorticoFullWindowRetiredOwners;
static NSLock *PorticoFullWindowRetiredLock;

/** The app's own supported set, read once the way the system reads it. */
static UIInterfaceOrientationMask PorticoFullWindowDefaultMask(void) {
  static UIInterfaceOrientationMask mask;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    NSDictionary *info = NSBundle.mainBundle.infoDictionary;
    NSArray *names = nil;
    if (UI_USER_INTERFACE_IDIOM() == UIUserInterfaceIdiomPad) names = info[@"UISupportedInterfaceOrientations~ipad"];
    if (![names isKindOfClass:NSArray.class] || names.count == 0) names = info[@"UISupportedInterfaceOrientations"];
    UIInterfaceOrientationMask value = 0;
    if ([names isKindOfClass:NSArray.class]) for (NSString *name in names) {
      if (![name isKindOfClass:NSString.class]) continue;
      if ([name isEqualToString:@"UIInterfaceOrientationPortrait"]) value |= UIInterfaceOrientationMaskPortrait;
      else if ([name isEqualToString:@"UIInterfaceOrientationPortraitUpsideDown"]) value |= UIInterfaceOrientationMaskPortraitUpsideDown;
      else if ([name isEqualToString:@"UIInterfaceOrientationLandscapeLeft"]) value |= UIInterfaceOrientationMaskLandscapeLeft;
      else if ([name isEqualToString:@"UIInterfaceOrientationLandscapeRight"]) value |= UIInterfaceOrientationMaskLandscapeRight;
    }
    mask = value ?: UIInterfaceOrientationMaskAllButUpsideDown;
  });
  return mask;
}

static void PorticoFullWindowRetireOwner(NSString *owner) {
  if (owner.length == 0) return;
  static dispatch_once_t once;
  dispatch_once(&once, ^{
    PorticoFullWindowRetiredOwners = [NSMutableSet new];
    PorticoFullWindowRetiredLock = [NSLock new];
  });
  [PorticoFullWindowRetiredLock lock];
  [PorticoFullWindowRetiredOwners addObject:owner];
  [PorticoFullWindowRetiredLock unlock];
}

static BOOL PorticoFullWindowConsumeRetiredOwner(NSString *owner) {
  if (owner.length == 0) return NO;
  [PorticoFullWindowRetiredLock lock];
  BOOL retired = [PorticoFullWindowRetiredOwners containsObject:owner];
  [PorticoFullWindowRetiredOwners removeObject:owner];
  [PorticoFullWindowRetiredLock unlock];
  return retired;
}

static void PorticoFullWindowRefresh(UIViewController *root) {
  if (@available(iOS 16.0, *)) [root setNeedsUpdateOfSupportedInterfaceOrientations];
}

static void PorticoFullWindowAsk(UIWindowScene *scene, UIInterfaceOrientationMask mask) {
  if (@available(iOS 16.0, *)) {
    if (!scene || scene.activationState == UISceneActivationStateUnattached) return;
    [scene requestGeometryUpdateWithPreferences:[[UIWindowSceneGeometryPreferencesIOS alloc] initWithInterfaceOrientations:mask] errorHandler:nil];
  }
}

UIInterfaceOrientationMask PorticoFullWindowSupportedOrientations(void) {
  return PorticoFullWindowOwner ? UIInterfaceOrientationMaskLandscape : PorticoFullWindowDefaultMask();
}

void PorticoFullWindowAdoptHostWindow(UIWindow *window) {
  if (![NSThread isMainThread]) {dispatch_async(dispatch_get_main_queue(), ^{PorticoFullWindowAdoptHostWindow(window);}); return;}
  PorticoFullWindowHostWindow = window;
}

@implementation PorticoFullWindow {
  BOOL _listening;
}

RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return NO; }
- (NSArray<NSString *> *)supportedEvents { return @[@"portico.fullwindow"]; }
- (void)startObserving {_listening = YES;}
- (void)stopObserving {_listening = NO;}

/** Only the OS/module capability, never whether a scene happens to exist right now. */
- (NSDictionary *)constantsToExport {
  if (@available(iOS 16.0, *)) return @{@"orientationSupported": @YES};
  return @{@"orientationSupported": @NO};
}

RCT_EXPORT_METHOD(enter:(NSString *)owner resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
  if (@available(iOS 16.0, *)) {} else {resolve(@NO); return;}
  __weak PorticoFullWindow *weakSelf = self;
  dispatch_async(dispatch_get_main_queue(), ^{
    PorticoFullWindow *module = weakSelf;
    if (!module || module.porticoRetired || PorticoFullWindowConsumeRetiredOwner(owner) || PorticoFullWindowOwner) {resolve(@NO); return;}
    UIWindow *window = PorticoFullWindowHostWindow;
    UIWindowScene *scene = window.windowScene;
    UIViewController *root = window.rootViewController;
    if (!window || window.isHidden || !scene || scene.activationState != UISceneActivationStateForegroundActive || !root) {resolve(@NO); return;}
    PorticoFullWindowOwner = owner;
    PorticoFullWindowOwnerModule = module;
    PorticoFullWindowWindow = window;
    PorticoFullWindowScene = scene;
    PorticoFullWindowRestoreMask = PorticoFullWindowDefaultMask();
    PorticoFullWindowRefresh(root);
    if (@available(iOS 16.0, *)) {
      __weak PorticoFullWindow *weakOwner = module;
      __weak UIWindowScene *weakScene = scene;
      NSString *issuedOwner = owner;
      [scene requestGeometryUpdateWithPreferences:[[UIWindowSceneGeometryPreferencesIOS alloc] initWithInterfaceOrientations:UIInterfaceOrientationMaskLandscape]
        errorHandler:^(NSError *error) {
          if (!error) return;
          dispatch_async(dispatch_get_main_queue(), ^{
            PorticoFullWindow *held = weakOwner;
            // Only the exact original request reports; a late refusal for A never names B.
            if (!held || !held->_listening || PorticoFullWindowOwnerModule != held || !weakScene || PorticoFullWindowScene != weakScene
              || ![PorticoFullWindowOwner isEqualToString:issuedOwner]) return;
            [held sendEventWithName:@"portico.fullwindow" body:@{@"owner": issuedOwner, @"error": error.localizedDescription ?: @"orientation_refused"}];
          });
        }];
    }
    resolve(@YES);
  });
}

RCT_EXPORT_METHOD(leave:(NSString *)owner) {
  PorticoFullWindowRetireOwner(owner);
  dispatch_async(dispatch_get_main_queue(), ^{
    PorticoFullWindowConsumeRetiredOwner(owner);
    if (PorticoFullWindowOwnerModule != self || !PorticoFullWindowOwner || ![PorticoFullWindowOwner isEqualToString:owner]) return;
    UIWindow *window = PorticoFullWindowWindow;
    UIWindowScene *scene = PorticoFullWindowScene;
    UIInterfaceOrientationMask restore = PorticoFullWindowRestoreMask;
    PorticoFullWindowOwner = nil;
    PorticoFullWindowOwnerModule = nil;
    PorticoFullWindowWindow = nil;
    PorticoFullWindowScene = nil;
    // Restored inside this same main turn, so no successor can capture the forced value.
    PorticoFullWindowRefresh(window.rootViewController);
    PorticoFullWindowAsk(scene, restore);
  });
}

- (void)invalidate {
  // The turn retains this instance: a weak capture could vanish before it runs, leaving the
  // owner string and the landscape mask occupied with nothing left to restore them.
  PorticoFullWindow *module = self;
  // Retired inside the turn, after any enter already posted: that enter ran first, and this
  // witness keeps every later turn for this instance out.
  dispatch_async(dispatch_get_main_queue(), ^{
    module.porticoRetired = YES;
    if (PorticoFullWindowOwnerModule != module) return;
    UIWindow *window = PorticoFullWindowWindow;
    UIWindowScene *scene = PorticoFullWindowScene;
    UIInterfaceOrientationMask restore = PorticoFullWindowRestoreMask;
    PorticoFullWindowOwner = nil;
    PorticoFullWindowOwnerModule = nil;
    PorticoFullWindowWindow = nil;
    PorticoFullWindowScene = nil;
    PorticoFullWindowRefresh(window.rootViewController);
    PorticoFullWindowAsk(scene, restore);
  });
  [super invalidate];
}

@end

#endif
