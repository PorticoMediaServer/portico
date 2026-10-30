package tv.getportico.portico.policy;

import static org.junit.Assert.assertTrue;

import java.util.Map;
import java.util.Set;
import org.junit.Test;

public final class PorticoListeningPolicyTest {
  private static Map<String, Object> session(String id, String state, int generation, int startMs) {
    return Map.of("id", id, "itemId", "item-" + id, "revision", "revision-" + generation + "-" + startMs + "-" + state,
        "state", state, "presentation", Map.of("generation", generation, "url", "/v1/media/grant-" + generation + "/audio", "startPositionMs", startMs));
  }

  @Test
  public void validatesPreferences() {
    Set<Double> rates = Set.of(1.0, 1.5, 2.0), minutes = Set.of(0.0, 30.0, 60.0);
    Map<String, Object> current = Map.of("bookRate", 1.5, "passoutMinutes", 30);
    assertTrue(PorticoListeningPolicy.validPreferences(current, rates, minutes));
    assertTrue(PorticoListeningPolicy.validPreferences(null, rates, minutes));
    assertTrue(!PorticoListeningPolicy.validPreferences(Map.of("bookRate", 1.5), rates, minutes));
    assertTrue(!PorticoListeningPolicy.validPreferences(Map.of("bookRate", Double.NaN, "passoutMinutes", 0), rates, minutes));
    assertTrue(!PorticoListeningPolicy.validPreferences(Map.of("bookRate", 9, "passoutMinutes", 0), rates, minutes));
    assertTrue(!PorticoListeningPolicy.validPreferences(Map.of("bookRate", 1.5, "passoutMinutes", 30, "autoplayNext", true), rates, minutes));
  }

  @Test
  public void projectsRates() {
    Map<String, Object> current = Map.of("bookRate", 1.5, "passoutMinutes", 30);
    assertTrue(PorticoListeningPolicy.preferredRate(false, current) == 1.0);
    assertTrue(PorticoListeningPolicy.preferredRate(false, Map.of("musicRate", 2.0)) == 1.0);
    assertTrue(PorticoListeningPolicy.preferredRate(true, current) == 1.5);
    assertTrue(PorticoListeningPolicy.preferredRate(true, Map.of("musicRate", 2.0)) == null);
  }

  @Test
  public void ownsContinuation() {
    assertTrue(PorticoListeningPolicy.mayAdvance(false, null));
    assertTrue(PorticoListeningPolicy.mayAdvance(false, Map.of("autoplay", true)));
    assertTrue(!PorticoListeningPolicy.mayAdvance(false, Map.of("autoplay", false)));
    assertTrue(!PorticoListeningPolicy.mayAdvance(true, Map.of("autoplay", true)));
  }

  @Test
  public void restrictsPlaybackRates() {
    Set<Double> rates = Set.of(1.0, 1.5, 2.0);
    assertTrue(!PorticoListeningPolicy.rateAllowed(false, 1.5, rates));
    assertTrue(PorticoListeningPolicy.rateAllowed(false, 1.0, rates));
    assertTrue(PorticoListeningPolicy.rateAllowed(true, 1.5, rates));
  }

  @Test
  public void validatesAudiobookPreferencesAndSkips() {
    Map<String,Object> book=Map.of("skipBackMs",60000,"skipForwardMs",90000,"resumeRewindMs",5000);
    assertTrue(PorticoListeningPolicy.validAudiobookPreferences(book));
    assertTrue(!PorticoListeningPolicy.validAudiobookPreferences(null));
    assertTrue(!PorticoListeningPolicy.validAudiobookPreferences(Map.of("skipBackMs",60000,"skipForwardMs",90000,"resumeRewindMs",true)));
    assertTrue(PorticoListeningPolicy.skipSeconds(true,book,false)==60 && PorticoListeningPolicy.skipSeconds(true,book,true)==90);
    assertTrue(PorticoListeningPolicy.skipSeconds(false,book,false)==30 && PorticoListeningPolicy.skipSeconds(false,book,true)==30);
    assertTrue(PorticoListeningPolicy.skipSeconds(true,null,false)==15 && PorticoListeningPolicy.skipSeconds(true,null,true)==30);
  }

  @Test
  public void boundsResumeRewind() {
    for(double amount:new double[]{0,5000,15000,30000}) {
      assertTrue(PorticoListeningPolicy.resumeDisplacement(299,100,amount)==0);
      assertTrue(PorticoListeningPolicy.resumeDisplacement(301,100,amount)==amount/1000);
      assertTrue(PorticoListeningPolicy.resumeDisplacement(3601,100,amount)==amount/1000);
      assertTrue(PorticoListeningPolicy.resumeDisplacement(3601,2,amount)==(amount>0?2:0));
    }
    assertTrue(PorticoListeningPolicy.resumeDisplacement(3601,100,7000)==0);
  }

  @Test
  public void reconcilesRemotePauseAndPlay() {
    Map<String, Object> playing = session("s", "playing", 1, 0);
    Map<String, Object> paused = session("s", "paused", 1, 0);
    PorticoListeningPolicy.SessionUpdate remotePause = PorticoListeningPolicy.reconcileSession(playing, paused, "playing", false, 45.0, null);
    assertTrue(remotePause != null && remotePause.intent.equals("paused") && !remotePause.patchWanted && remotePause.dropPrepared && remotePause.seekSeconds == null);
    PorticoListeningPolicy.SessionUpdate remotePlay = PorticoListeningPolicy.reconcileSession(paused, playing, "paused", false, 45.0, null);
    assertTrue(remotePlay != null && remotePlay.intent.equals("playing") && !remotePlay.patchWanted && remotePlay.seekSeconds == null);
  }

  @Test
  public void honorsServerSeeksWithAndWithoutGenerationChanges() {
    Map<String, Object> playing = session("s", "playing", 1, 0);
    // A server seek changes the target, including direct audio with no generation bump.
    for (int generation : new int[]{1, 2}) {
      PorticoListeningPolicy.SessionUpdate seek = PorticoListeningPolicy.reconcileSession(playing, session("s", "paused", generation, 90000), "playing", false, 45.0, null);
      assertTrue(seek != null && seek.intent.equals("paused") && seek.seekSeconds == 90.0 && seek.dropPrepared);
      // A stale local PATCH still owns its latest pause/resume, but must not overwrite a server seek with the old position.
      PorticoListeningPolicy.SessionUpdate conflict = PorticoListeningPolicy.reconcileSession(playing, session("s", "paused", generation, 90000), "playing", true, 45.0, null);
      assertTrue(conflict != null && conflict.intent.equals("playing") && conflict.patchWanted && conflict.seekSeconds == 90.0);
    }
  }

  @Test
  public void preservesNewerLocalIntent() {
    Map<String, Object> playing = session("s", "playing", 1, 0);
    Map<String, Object> paused = session("s", "paused", 1, 0);
    PorticoListeningPolicy.SessionUpdate confirmed = PorticoListeningPolicy.reconcileSession(playing, paused, "paused", false, 45.0, null);
    assertTrue(confirmed != null && !confirmed.patchWanted && confirmed.seekSeconds == null);
    PorticoListeningPolicy.SessionUpdate newerLocalIntent = PorticoListeningPolicy.reconcileSession(playing, paused, "playing", true, 45.0, null);
    assertTrue(newerLocalIntent != null && newerLocalIntent.intent.equals("playing") && newerLocalIntent.patchWanted);
  }

  @Test
  public void retainsPositionAcrossReplans() {
    Map<String, Object> playing = session("s", "playing", 1, 0);
    Map<String, Object> replanned = session("s", "playing", 2, 0);
    PorticoListeningPolicy.SessionUpdate replan = PorticoListeningPolicy.reconcileSession(playing, replanned, "playing", false, 45.0, null);
    assertTrue(replan != null && replan.seekSeconds == 45.0 && replan.dropPrepared);
    PorticoListeningPolicy.SessionUpdate pendingSeek = PorticoListeningPolicy.reconcileSession(playing, replanned, "playing", false, 45.0, 75.0);
    assertTrue(pendingSeek != null && pendingSeek.seekSeconds == 75.0);
    PorticoListeningPolicy.SessionUpdate unobserved = PorticoListeningPolicy.reconcileSession(playing, replanned, "playing", false, null, null);
    assertTrue(unobserved != null && unobserved.seekSeconds == 0.0);
    PorticoListeningPolicy.SessionUpdate repeated = PorticoListeningPolicy.reconcileSession(replanned, replanned, "playing", false, 46.0, null);
    assertTrue(repeated != null && repeated.seekSeconds == null && !repeated.dropPrepared);
  }

  @Test
  public void rejectsStaleOrForeignSessions() {
    Map<String, Object> playing = session("s", "playing", 1, 0);
    Map<String, Object> replanned = session("s", "playing", 2, 0);
    assertTrue(PorticoListeningPolicy.reconcileSession(replanned, playing, "playing", false, 45.0, null) == null);
    assertTrue(PorticoListeningPolicy.reconcileSession(playing, session("other", "playing", 2, 0), "playing", false, 45.0, null) == null);
    assertTrue(PorticoListeningPolicy.reconcileSession(playing, session("s", "ended", 2, 0), "playing", false, 45.0, null) == null);
    assertTrue(PorticoListeningPolicy.reconcileSession(playing, session("s", "unknown", 2, 0), "playing", false, 45.0, null) == null);
  }

  @Test
  public void retriesEngineFailuresOnSystemPlay() {
    // System Play after an engine error takes the same reset/seek branch as Retry. A safety pause alone does not.
    assertTrue(PorticoListeningPolicy.resumeNeedsRetry(false, true));
    assertTrue(PorticoListeningPolicy.resumeNeedsRetry(true, false));
    assertTrue(PorticoListeningPolicy.resumeNeedsRetry(true, true));
    assertTrue(!PorticoListeningPolicy.resumeNeedsRetry(false, false));
  }
}
