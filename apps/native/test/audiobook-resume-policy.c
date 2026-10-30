#include "../Native/PorticoListeningPolicy.h"
#include <assert.h>
int main(void){
 for(int i=0;i<4;i++){
  const double choice[]={0,5000,15000,30000};
  assert(PorticoAudiobookResumeDisplacement(299,100,choice[i])==0);
  assert(PorticoAudiobookResumeDisplacement(301,100,choice[i])==choice[i]/1000);
  assert(PorticoAudiobookResumeDisplacement(3601,100,choice[i])==choice[i]/1000);
  assert(PorticoAudiobookResumeDisplacement(3601,2,choice[i])==(choice[i]?2:0));
 }
 assert(PorticoAudiobookResumeDisplacement(3601,100,7000)==0);
 assert(PorticoAudiobookResumeDisplacement(3601,NAN,5000)==0);
 return 0;
}
