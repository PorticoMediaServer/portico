#ifndef PorticoAudioEffects_h
#define PorticoAudioEffects_h
#import <Foundation/Foundation.h>
#include <math.h>

static inline BOOL PorticoAudioEffectNumber(id value,double minimum,double maximum) {
 return [value isKindOfClass:NSNumber.class]&&CFGetTypeID((__bridge CFTypeRef)value)!=CFBooleanGetTypeID()&&isfinite([value doubleValue])&&[value doubleValue]>=minimum&&[value doubleValue]<=maximum;
}
static inline BOOL PorticoAudioEnvelopeValid(id value) {
 if(![value isKindOfClass:NSDictionary.class]||!PorticoAudioEffectNumber(value[@"intervalMs"],100,100))return NO;
 for(NSString *key in @[@"startLufs",@"endLufs"]){
  id values=value[key];if(![values isKindOfClass:NSArray.class]||[values count]>30)return NO;
  for(id point in values)if(!PorticoAudioEffectNumber(point,-120,10))return NO;
 }
 return YES;
}
/** These are measured/baked producer facts, never additional client gain controls. */
static inline BOOL PorticoAudioPlanFactsValid(NSDictionary *plan) {
 id bitrate=plan[@"sourceBitrateBps"];
 if(bitrate&&(!PorticoAudioEffectNumber(bitrate,0,9007199254740991.0)||floor([bitrate doubleValue])!=[bitrate doubleValue]))return NO;
 for(NSString *key in @[@"conversionGainDb",@"loudnessAppliedDb"])if(plan[key]&&!PorticoAudioEffectNumber(plan[key],-60,30))return NO;
 if(plan[@"loudnessAppliedDb"]&&plan[@"gain"])return NO;
 if(plan[@"loudnessEnvelope"]&&!PorticoAudioEnvelopeValid(plan[@"loudnessEnvelope"]))return NO;
 return YES;
}
static inline double PorticoAudioGainLinear(NSDictionary *plan,NSDictionary *settings) {
 if(plan[@"loudnessAppliedDb"])return 1;
 NSDictionary *gain=[plan[@"gain"] isKindOfClass:NSDictionary.class]?plan[@"gain"]:nil;
 NSString *mode=settings[@"normalization"];
 if(!gain||![@[@"track",@"album"] containsObject:mode])return 1;
 BOOL album=[mode isEqual:@"album"]&&gain[@"albumDb"];
 id db=album?gain[@"albumDb"]:gain[@"trackDb"];
 if(!PorticoAudioEffectNumber(db,-60,30))return 1;
 id peak=album?(gain[@"albumPeak"]?:gain[@"trackPeak"]):gain[@"trackPeak"];
 BOOL measuredPeak=PorticoAudioEffectNumber(peak,.000001,64);
 double linear=pow(10,[db doubleValue]/20);
 if(measuredPeak&&[peak doubleValue]*linear>1)linear=1/[peak doubleValue];
 return fmax(0,fmin(linear,measuredPeak&&![settings[@"neverBoost"] isEqual:@YES]?32:1));
}
/** The coefficient must actually differ at the renderer's Float32 precision.
 * The renderer still reports applied only after this song's buffers are heard. */
static inline BOOL PorticoAudioHasLocalLeveling(NSDictionary *plan,NSDictionary *settings) {
 return (float)PorticoAudioGainLinear(plan,settings)!=1.0f;
}
static inline double PorticoAudioLoudnessMean(NSArray<NSNumber *> *values) {
 double power=0;for(NSNumber *value in values)power+=pow(10,value.doubleValue/10);
 return 10*log10(power/values.count);
}
/** Same rule as the canonical smoothTransitionSeconds: actual measured head/tail
 * contrast shapes the overlap duration. Missing measurements keep the fixed fade. */
static inline double PorticoAudioTransitionSeconds(NSDictionary *current,NSDictionary *next,double maximum,BOOL sameAlbum,BOOL smooth) {
 if(sameAlbum)return 0;
 if(!isfinite(maximum)||maximum<0||maximum>12)return NAN;
 if(!smooth)return maximum;
 NSDictionary *tail=current[@"loudnessEnvelope"],*head=next[@"loudnessEnvelope"];
 if(!PorticoAudioEnvelopeValid(tail)||!PorticoAudioEnvelopeValid(head))return maximum;
 NSArray *end=tail[@"endLufs"],*start=head[@"startLufs"];
 if(!end.count||!start.count)return maximum;
 double contrast=fabs(PorticoAudioLoudnessMean(end)-PorticoAudioLoudnessMean(start));
 return fmin(maximum,fmax(.25,maximum/(1+contrast/12)));
}
#endif
