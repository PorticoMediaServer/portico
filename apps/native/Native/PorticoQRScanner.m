#import <React/RCTBridgeModule.h>
#import <React/RCTInvalidating.h>
#import <React/RCTUtils.h>
#import <AVFoundation/AVFoundation.h>
#import <UIKit/UIKit.h>

@interface PorticoQRScanController : UIViewController <AVCaptureMetadataOutputObjectsDelegate>
@property(nonatomic,copy) NSString *scanTitle;
@property(nonatomic,copy) NSString *cancelTitle;
@property(nonatomic,copy) void (^answer)(NSString *, NSString *);
@property(nonatomic,strong) AVCaptureSession *session;
@property(nonatomic,strong) AVCaptureVideoPreviewLayer *preview;
@property(nonatomic,strong) dispatch_queue_t captureQueue;
@property(atomic,assign) BOOL retired;
@property(nonatomic,copy) BOOL (^openingCurrent)(void);
- (void)retire;
@end

@implementation PorticoQRScanController
- (void)viewDidLoad {
 [super viewDidLoad]; self.view.backgroundColor = UIColor.blackColor;
 self.captureQueue = dispatch_queue_create("tv.getportico.qr-camera", DISPATCH_QUEUE_SERIAL);
 self.session = [AVCaptureSession new];
 self.preview = [AVCaptureVideoPreviewLayer layerWithSession:self.session]; self.preview.videoGravity = AVLayerVideoGravityResizeAspectFill;
 [self.view.layer addSublayer:self.preview];
 UILabel *title = [UILabel new]; title.text = self.scanTitle; title.textColor = UIColor.whiteColor; title.numberOfLines = 0; title.textAlignment = NSTextAlignmentCenter;
 title.font = [UIFont preferredFontForTextStyle:UIFontTextStyleBody]; title.adjustsFontForContentSizeCategory = YES;
 UIButton *cancel = [UIButton buttonWithType:UIButtonTypeSystem]; [cancel setTitle:self.cancelTitle forState:UIControlStateNormal];
 cancel.backgroundColor = UIColor.blackColor; [cancel setTitleColor:UIColor.whiteColor forState:UIControlStateNormal];
 cancel.titleLabel.font = [UIFont preferredFontForTextStyle:UIFontTextStyleSubheadline]; cancel.titleLabel.adjustsFontForContentSizeCategory = YES;
 [cancel addTarget:self action:@selector(cancelScan) forControlEvents:UIControlEventTouchUpInside];
 for (UIView *v in @[title, cancel]) {v.translatesAutoresizingMaskIntoConstraints = NO; [self.view addSubview:v];}
 [NSLayoutConstraint activateConstraints:@[
  [title.topAnchor constraintEqualToAnchor:self.view.safeAreaLayoutGuide.topAnchor constant:20], [title.leadingAnchor constraintEqualToAnchor:self.view.leadingAnchor constant:24], [title.trailingAnchor constraintEqualToAnchor:self.view.trailingAnchor constant:-24],
  [cancel.bottomAnchor constraintEqualToAnchor:self.view.safeAreaLayoutGuide.bottomAnchor constant:-16], [cancel.centerXAnchor constraintEqualToAnchor:self.view.centerXAnchor], [cancel.heightAnchor constraintGreaterThanOrEqualToConstant:48], [cancel.widthAnchor constraintGreaterThanOrEqualToConstant:120]
 ]];
 [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(cameraFailed:) name:AVCaptureSessionRuntimeErrorNotification object:self.session];
 dispatch_async(self.captureQueue, ^{
  if (self.retired || !self.openingCurrent || !self.openingCurrent()) return;
  AVCaptureDevice *device = [AVCaptureDevice defaultDeviceWithDeviceType:AVCaptureDeviceTypeBuiltInWideAngleCamera mediaType:AVMediaTypeVideo position:AVCaptureDevicePositionBack];
  if (!device) device = [AVCaptureDevice defaultDeviceWithMediaType:AVMediaTypeVideo];
  NSError *error = nil; AVCaptureDeviceInput *input = device ? [AVCaptureDeviceInput deviceInputWithDevice:device error:&error] : nil;
  if (self.retired || !self.openingCurrent || !self.openingCurrent()) return;
  AVCaptureMetadataOutput *output = [AVCaptureMetadataOutput new];
  if (!input || error || ![self.session canAddInput:input] || ![self.session canAddOutput:output]) { [self cameraFailed:nil]; return; }
  [self.session beginConfiguration];
  if ([self.session canSetSessionPreset:AVCaptureSessionPreset1280x720]) self.session.sessionPreset = AVCaptureSessionPreset1280x720;
  [self.session addInput:input]; [self.session addOutput:output];
  if (![output.availableMetadataObjectTypes containsObject:AVMetadataObjectTypeQRCode]) { [self.session commitConfiguration]; [self cameraFailed:nil]; return; }
  output.metadataObjectTypes = @[AVMetadataObjectTypeQRCode]; [output setMetadataObjectsDelegate:self queue:dispatch_get_main_queue()];
  [self.session commitConfiguration]; if (!self.retired && self.openingCurrent && self.openingCurrent()) [self.session startRunning];
 });
}
- (void)viewDidLayoutSubviews {
 [super viewDidLayoutSubviews]; self.preview.frame = self.view.bounds;
 AVCaptureConnection *connection = self.preview.connection;
 if (connection.isVideoOrientationSupported) {
  UIInterfaceOrientation orientation = self.view.window.windowScene.interfaceOrientation;
  connection.videoOrientation = orientation == UIInterfaceOrientationLandscapeLeft ? AVCaptureVideoOrientationLandscapeLeft : orientation == UIInterfaceOrientationLandscapeRight ? AVCaptureVideoOrientationLandscapeRight : orientation == UIInterfaceOrientationPortraitUpsideDown ? AVCaptureVideoOrientationPortraitUpsideDown : AVCaptureVideoOrientationPortrait;
 }
}
- (void)finish:(NSString *)raw error:(NSString *)error {
 if (self.retired) return;
 if (!self.openingCurrent || !self.openingCurrent()) {raw = nil; error = nil;}
 void (^answer)(NSString *, NSString *) = self.answer; self.answer = nil; [self retire]; if (answer) answer(raw, error);
}
- (void)cancelScan { [self finish:nil error:nil]; }
- (void)cameraFailed:(NSNotification *)notification { dispatch_async(dispatch_get_main_queue(), ^{[self finish:nil error:@"qr_camera_unavailable"];}); }
- (void)captureOutput:(AVCaptureOutput *)output didOutputMetadataObjects:(NSArray<__kindof AVMetadataObject *> *)objects fromConnection:(AVCaptureConnection *)connection {
 if (self.retired || !self.openingCurrent || !self.openingCurrent()) return;
 for (AVMetadataObject *object in objects) {
  if (![object.type isEqualToString:AVMetadataObjectTypeQRCode] || ![object isKindOfClass:AVMetadataMachineReadableCodeObject.class]) continue;
  NSString *raw = ((AVMetadataMachineReadableCodeObject *)object).stringValue; if (!raw) continue;
  NSUInteger bytes = [raw lengthOfBytesUsingEncoding:NSUTF8StringEncoding];
  if (bytes == 0 || bytes > 8192) [self finish:nil error:@"qr_too_large"]; else [self finish:raw error:nil];
  return;
 }
}
- (void)retire {
 if (self.retired) return;
 self.retired = YES; self.answer = nil; [[NSNotificationCenter defaultCenter] removeObserver:self];
 AVCaptureSession *owned = self.session;
 if (self.captureQueue) dispatch_async(self.captureQueue, ^{if (owned.isRunning) [owned stopRunning];});
}
- (void)viewDidDisappear:(BOOL)animated { [super viewDidDisappear:animated]; [self finish:nil error:nil]; }
- (void)dealloc { [[NSNotificationCenter defaultCenter] removeObserver:self]; }
@end

@interface PorticoQRPending : NSObject
@property(nonatomic,copy) NSString *requestId;
@property(nonatomic,copy) RCTPromiseResolveBlock resolve;
@property(nonatomic,copy) RCTPromiseRejectBlock reject;
@property(nonatomic,strong) PorticoQRScanController *controller;
@property(atomic,assign) BOOL active;
@property(nonatomic,assign) BOOL ready;
@property(nonatomic,copy) NSString *scanTitle;
@property(nonatomic,copy) NSString *cancelTitle;
@end
@implementation PorticoQRPending @end

@interface PorticoQRScanner : NSObject <RCTBridgeModule, RCTInvalidating>
@property(atomic,strong) PorticoQRPending *pending;
@property(atomic,assign) BOOL invalidated;
@end
static __weak PorticoQRScanner *cameraOwner;
@implementation PorticoQRScanner
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (instancetype)init { if ((self = [super init])) { [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(backgrounded:) name:UIApplicationDidEnterBackgroundNotification object:nil]; [[NSNotificationCenter defaultCenter] addObserver:self selector:@selector(becameActive:) name:UIApplicationDidBecomeActiveNotification object:nil]; } return self; }
- (void)settle:(PorticoQRPending *)owned raw:(NSString *)raw error:(NSString *)error {
 if (self.pending != owned || !owned.active) return;
 owned.active = NO; self.pending = nil; if (cameraOwner == self) cameraOwner = nil;
 RCTPromiseResolveBlock resolve = owned.resolve; RCTPromiseRejectBlock reject = owned.reject; owned.resolve = nil; owned.reject = nil;
 PorticoQRScanController *controller = owned.controller; owned.controller = nil;
 [controller retire]; [controller dismissViewControllerAnimated:YES completion:nil];
 if (self.invalidated) resolve(NSNull.null);
 else if (error) reject(error, @"QR scan did not complete.", nil); else resolve(raw ?: NSNull.null);
}
- (void)present:(PorticoQRPending *)owned title:(NSString *)title cancel:(NSString *)cancel {
 if (self.invalidated || self.pending != owned || !owned.active) return;
 if (UIApplication.sharedApplication.applicationState == UIApplicationStateBackground) { [self settle:owned raw:nil error:nil]; return; }
 if (UIApplication.sharedApplication.applicationState != UIApplicationStateActive) { owned.ready = YES; return; }
 if (owned.controller) return; owned.ready = NO;
 UIViewController *presenter = RCTPresentedViewController();
 if (!presenter || !presenter.view.window) { [self settle:owned raw:nil error:@"qr_camera_unavailable"]; return; }
 PorticoQRScanController *controller = [PorticoQRScanController new]; controller.scanTitle = title; controller.cancelTitle = cancel;
 controller.modalPresentationStyle = UIModalPresentationFullScreen; owned.controller = controller;
 __weak PorticoQRScanner *weakSelf = self; __weak PorticoQRPending *weakOwned = owned;
 controller.openingCurrent = ^BOOL {
  PorticoQRScanner *owner = weakSelf; PorticoQRPending *original = weakOwned;
  return owner && original && !owner.invalidated && owner.pending == original && original.active;
 };
 controller.answer = ^(NSString *raw, NSString *error) { PorticoQRPending *original = weakOwned; if (original) [weakSelf settle:original raw:raw error:error]; };
 if (self.invalidated || self.pending != owned || !owned.active) { [controller retire]; return; }
 [presenter presentViewController:controller animated:YES completion:nil];
}
RCT_REMAP_METHOD(scan, scan:(NSString *)requestId title:(NSString *)title cancel:(NSString *)cancel resolver:(RCTPromiseResolveBlock)resolve rejecter:(RCTPromiseRejectBlock)reject) {
 if (self.invalidated) { reject(@"qr_camera_unavailable", @"Scanner module is closed.", nil); return; }
 NSRegularExpression *pattern = [NSRegularExpression regularExpressionWithPattern:@"^[a-f0-9]{32}$" options:0 error:nil];
 if (![requestId isKindOfClass:NSString.class] || [pattern numberOfMatchesInString:requestId options:0 range:NSMakeRange(0, requestId.length)] != 1 || title.length == 0 || title.length > 200 || cancel.length == 0 || cancel.length > 100) { reject(@"qr_invalid_request", @"Invalid scanner opening.", nil); return; }
 if (self.pending || cameraOwner.pending.active) { reject(@"qr_busy", @"A QR scan is already open.", nil); return; }
 PorticoQRPending *owned = [PorticoQRPending new]; owned.requestId = requestId; owned.resolve = resolve; owned.reject = reject; owned.active = YES; owned.scanTitle = title; owned.cancelTitle = cancel; self.pending = owned; cameraOwner = self;
 if (self.invalidated) { [self settle:owned raw:nil error:nil]; return; }
 AVAuthorizationStatus status = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
 if (status == AVAuthorizationStatusAuthorized) [self present:owned title:title cancel:cancel];
 else if (status == AVAuthorizationStatusNotDetermined && !self.invalidated && self.pending == owned && owned.active) [AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL granted) {
  dispatch_async(dispatch_get_main_queue(), ^{ if (self.invalidated || self.pending != owned || !owned.active) return; if (granted) [self present:owned title:title cancel:cancel]; else [self settle:owned raw:nil error:@"qr_permission_denied"]; });
 }];
 else [self settle:owned raw:nil error:@"qr_permission_denied"];
}
RCT_EXPORT_METHOD(cancel:(NSString *)requestId) { PorticoQRPending *owned = self.pending; if (owned && [owned.requestId isEqualToString:requestId]) [self settle:owned raw:nil error:nil]; }
- (void)becameActive:(NSNotification *)notification { PorticoQRPending *owned = self.pending; if (!self.invalidated && owned.active && owned.ready) [self present:owned title:owned.scanTitle cancel:owned.cancelTitle]; }
- (void)backgrounded:(NSNotification *)notification { PorticoQRPending *owned = self.pending; if (owned) [self settle:owned raw:nil error:nil]; }
- (void)invalidate {
 // Irrevocable before dispatch: held starts, permission callbacks and the
 // capture queue all observe this flag without waiting for main cleanup.
 self.invalidated = YES;
 PorticoQRPending *original = self.pending;
 [[NSNotificationCenter defaultCenter] removeObserver:self];
 dispatch_async(dispatch_get_main_queue(), ^{if (original) [self settle:original raw:nil error:nil];});
}
- (void)dealloc { [[NSNotificationCenter defaultCenter] removeObserver:self]; }
@end
