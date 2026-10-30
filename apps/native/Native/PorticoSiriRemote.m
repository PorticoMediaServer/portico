#import <React/RCTEventEmitter.h>
#import <React/RCTBridgeModule.h>
#import <GameController/GameController.h>

/**
 * The Siri Remote touch surface as an absolute finger position, for scrubbing (FEAT-12, OS-05).
 *
 * React Native's TV events report presses and coarse swipes, which is enough for focus but not
 * for "rest a finger and slide through the film". The remote also appears to the app as a
 * GameController micro gamepad whose d-pad can report where the finger is (-1…1 on each axis).
 * This module listens only while JavaScript enables it (the player's timeline has focus), turns
 * that into began/changed/ended touches at ~30 Hz plus quick-flick swipes, and restores the
 * gamepad's default reporting when disabled, so focus navigation elsewhere is untouched.
 */
@interface PorticoSiriRemote : RCTEventEmitter <RCTBridgeModule> @end

@implementation PorticoSiriRemote {
  BOOL _listening, _enabled, _touching;
  float _startX, _startY, _lastX, _lastY;
  NSTimeInterval _startTime, _lastEmit;
  NSHashTable<GCMicroGamepad *> *_attached;
}
RCT_EXPORT_MODULE()
+ (BOOL)requiresMainQueueSetup { return YES; }
- (dispatch_queue_t)methodQueue { return dispatch_get_main_queue(); }
- (NSArray<NSString *> *)supportedEvents { return @[@"PorticoSiriRemoteTouch", @"PorticoSiriRemoteSwipe"]; }
- (void)startObserving { _listening = YES; }
- (void)stopObserving { _listening = NO; }

- (instancetype)init {
  if ((self = [super init])) {
    _attached = [NSHashTable weakObjectsHashTable];
    [NSNotificationCenter.defaultCenter addObserver:self selector:@selector(connected:) name:GCControllerDidConnectNotification object:nil];
  }
  return self;
}

RCT_EXPORT_METHOD(setEnabled:(BOOL)enabled) {
  if (enabled == _enabled) return;
  _enabled = enabled; _touching = NO;
  if (enabled) { for (GCController *controller in GCController.controllers) [self attach:controller]; }
  else [self detachAll];
}

- (void)connected:(NSNotification *)note { if (_enabled) [self attach:note.object]; }

- (void)attach:(GCController *)controller {
  GCMicroGamepad *pad = controller.microGamepad;
  if (!pad || [_attached containsObject:pad]) return;
  pad.reportsAbsoluteDpadValues = YES;
  pad.allowsRotation = NO;
  __weak PorticoSiriRemote *weakSelf = self;
  pad.dpad.valueChangedHandler = ^(GCControllerDirectionPad *dpad, float x, float y) {
    dispatch_async(dispatch_get_main_queue(), ^{ [weakSelf moved:x y:y]; });
  };
  [_attached addObject:pad];
}

- (void)detachAll {
  for (GCMicroGamepad *pad in _attached.allObjects) { pad.dpad.valueChangedHandler = nil; pad.reportsAbsoluteDpadValues = NO; }
  [_attached removeAllObjects];
}

/** With absolute reporting, (0, 0) means no finger on the surface. */
- (void)moved:(float)x y:(float)y {
  if (!_enabled) return;
  NSTimeInterval now = NSProcessInfo.processInfo.systemUptime;
  BOOL lifted = fabsf(x) < 0.0001f && fabsf(y) < 0.0001f;
  if (lifted) {
    if (!_touching) return;
    _touching = NO;
    [self emit:@"PorticoSiriRemoteTouch" body:@{@"phase": @"ended", @"x": @(_lastX), @"y": @(_lastY)}];
    float dx = _lastX - _startX, dy = _lastY - _startY;
    if (now - _startTime < 0.35 && MAX(fabsf(dx), fabsf(dy)) > 0.6f) {
      NSString *direction = fabsf(dx) > fabsf(dy) ? (dx > 0 ? @"right" : @"left") : (dy > 0 ? @"up" : @"down");
      [self emit:@"PorticoSiriRemoteSwipe" body:@{@"direction": direction}];
    }
    return;
  }
  _lastX = x; _lastY = y;
  if (!_touching) {
    _touching = YES; _startX = x; _startY = y; _startTime = now; _lastEmit = now;
    [self emit:@"PorticoSiriRemoteTouch" body:@{@"phase": @"began", @"x": @(x), @"y": @(y)}];
    return;
  }
  if (now - _lastEmit < 1.0 / 30.0) return;
  _lastEmit = now;
  [self emit:@"PorticoSiriRemoteTouch" body:@{@"phase": @"changed", @"x": @(x), @"y": @(y)}];
}

- (void)emit:(NSString *)name body:(NSDictionary *)body { if (_listening) [self sendEventWithName:name body:body]; }

- (void)invalidate {
  [NSNotificationCenter.defaultCenter removeObserver:self];
  dispatch_async(dispatch_get_main_queue(), ^{ self->_enabled = NO; [self detachAll]; });
  [super invalidate];
}
@end
