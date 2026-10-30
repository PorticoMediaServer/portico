import type {CapabilityDocument} from '../../../../../packages/client-core/src/client-profile';
import type {CapabilityPublishResult} from '../../../../../packages/client-core/src/playback-v1/options';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';

type Publisher = Readonly<{publish: (document: CapabilityDocument) => Promise<CapabilityPublishResult>}>;

/** FMT-02: a start proceeds only once the newest desired capability document is
 * acknowledged. A failure is retried once for the same document; a superseded
 * publication never stands in for its successor. */
export class NativeCapabilityReadiness {
  private publishing: Promise<void> = Promise.resolve();
  private generation = 0;
  private failed = false;
  private readonly publisher: Publisher;
  private readonly closed: () => boolean;
  constructor(publisher: Publisher, closed: () => boolean) {this.publisher = publisher; this.closed = closed;}

  publish(build: () => Promise<CapabilityDocument | undefined>): Promise<void> {
    const generation = ++this.generation;
    const current = () => !this.closed() && generation === this.generation;
    this.publishing = this.publishing.then(async () => {
      if (!current()) return; // A newer request is already queued behind this one.
      let result: CapabilityPublishResult = 'failed';
      try {
        const document = await build();
        if (!document || !current()) return;
        result = await this.publisher.publish(document);
        if (result === 'failed' && current()) result = await this.publisher.publish(document);
      } catch {result = 'failed';}
      if (generation === this.generation) this.failed = result === 'failed';
    });
    return this.publishing;
  }

  ready = async (): Promise<void> => {
    for (;;) {const pending = this.publishing; await pending; if (pending === this.publishing) break;}
    if (this.failed) throw new Error(serviceText('queuePlayer.startFailed'));
  };
}
