import {configureDestination} from './output';
/**
 * A media element keeps music attached to media keys and the lock screen (B-17.19).
 *
 * The element retains browser media ownership for the keyboard's media keys, headset buttons,
 * media hub and lock screen. It receives silence from a zero-gain branch of the same context. The music
 * itself goes straight to the speakers: the element's independent live-stream clock must not
 * drop or repeat the samples. If the element cannot be set up or played, only the controls give
 * way; the engine keeps its direct speaker connection.
 * A pause that reaches the element from outside the page (the system took the audio) is reported,
 * so the player's intent follows it, like a pause of the video element.
 */
export type CarrierElement = {
  srcObject: MediaStream | null;
  readonly paused: boolean;
  play(): Promise<void>;
  pause(): void;
  setAttribute(name: string, value: string): void;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
};
type StreamContext = Pick<AudioContext, 'destination'> & Partial<Pick<AudioContext, 'createMediaStreamDestination' | 'createGain'>>;

export class MediaCarrier {
  private readonly output: AudioNode;
  private readonly context: StreamContext;
  private element: CarrierElement | null = null;
  private stream: MediaStreamAudioDestinationNode | null = null;
  private controlGain: GainNode | null = null;
  private ours = false;
  private disposed = false;
  private playAttempt = 0;
  private readonly onPause: () => void;
  private readonly paused = () => {
    // The page's own pause is not news; any other while carrying is the system's or the viewer's.
    if (this.ours || this.disposed || !this.element?.paused) return;
    this.onPause();
  };

  /** `output` is the engine's last node; speakers use its clock, while the element owns controls. */
  constructor(context: StreamContext, output: AudioNode, onPause: () => void, make: () => CarrierElement | null = defaultElement) {
    this.context = context; this.output = output; this.onPause = onPause;
    let element: CarrierElement | null = null, stream: MediaStreamAudioDestinationNode | null = null;
    try {
      // MediaStreamAudioDestination defaults to stereo. Surround uses the real endpoint.
      if (configureDestination(context.destination) <= 2 && context.createMediaStreamDestination && context.createGain) {
        element = make();
        if (element) {
          stream = context.createMediaStreamDestination();
          element.setAttribute('playsinline', '');
          element.srcObject = stream.stream;
          this.controlGain = context.createGain();
          this.controlGain.gain.value = 0;
          this.controlGain.connect(stream);
        }
      }
    } catch {
      this.controlGain?.disconnect();
      this.controlGain = null;
      if (element) element.srcObject = null;
      element = null; stream = null;
    }
    output.connect(context.destination);
    if (element && stream && this.controlGain) {
      this.element = element; this.stream = stream;
      output.connect(this.controlGain);
      element.addEventListener('pause', this.paused);
    }
  }

  /** Re-read the actual endpoint when the selected/default output changes. Never fold surround to stereo. */
  refreshOutput(): number {
    const channels = configureDestination(this.context.destination);
    if (channels > 2) this.direct();
    return channels;
  }

  /** Whether the element is carrying media keys and lock-screen controls. Heard audio stays on the context clock. */
  get carrying(): boolean { return this.element !== null; }

  /** Playing: the element plays its stream. One that refuses gives the speakers the audio directly. */
  play(onRefused?: (error: unknown) => void): void {
    const element = this.element;
    if (!element || !element.paused) return;
    this.ours = false;
    const attempt = ++this.playAttempt;
    void element.play().catch(error => {
      if (this.disposed || this.element !== element || attempt !== this.playAttempt) return;
      this.direct();
      onRefused?.(error);
    });
  }

  /** Paused by the page: the element follows, without that being reported back as the viewer's pause. */
  pause(): void {
    ++this.playAttempt;
    const element = this.element;
    if (!element || element.paused) return;
    this.ours = true;
    // HTML pause events arrive asynchronously; keep this intent until play is requested.
    element.pause();
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.release();
    try { this.output.disconnect(); } catch { /* already */ }
  }

  /** From now on the audio goes straight to the speakers. */
  private direct() {
    if (this.disposed || !this.element) return;
    this.release();
  }

  private release() {
    ++this.playAttempt;
    const element = this.element;
    if (!element) return;
    this.element = null;
    element.removeEventListener('pause', this.paused);
    this.ours = true;
    try { element.pause(); } catch { /* gone */ }
    element.srcObject = null;
    if (this.controlGain) { try { this.output.disconnect(this.controlGain); } catch { /* already */ } }
    try { this.stream?.disconnect(); } catch { /* already */ }
    this.stream = null;
    this.controlGain?.disconnect();
    this.controlGain = null;
  }
}

function defaultElement(): CarrierElement | null {
  if (typeof document === 'undefined' || typeof document.createElement !== 'function') return null;
  return document.createElement('audio') as unknown as CarrierElement;
}
