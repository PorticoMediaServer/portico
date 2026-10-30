import {configureDestination} from './output';
/**
 * The media element music decoded on the device plays through (B-17.19).
 *
 * Browsers give the keyboard's media keys, a headset's buttons, the browser's media hub and the
 * lock screen to a page that is playing a media element; audio from an AudioContext alone does
 * not count in Chrome, Firefox or Safari. So the engine's output goes to a stream the page plays in
 * an ordinary `<audio>` element, and Media Session then works for music as it does for films.
 *
 * Where that cannot be set up (no MediaStream destination, or the element refuses to play), the
 * output goes straight to the speakers as before: audio is never lost for the sake of the keys.
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
type StreamContext = Pick<AudioContext, 'destination'> & {createMediaStreamDestination?: () => MediaStreamAudioDestinationNode};

export class MediaCarrier {
  private readonly output: AudioNode;
  private readonly context: StreamContext;
  private element: CarrierElement | null = null;
  private stream: MediaStreamAudioDestinationNode | null = null;
  private ours = false;
  private disposed = false;
  private playAttempt = 0;
  private readonly onPause: () => void;
  private readonly paused = () => {
    // The page's own pause is not news; any other while carrying is the system's or the viewer's.
    if (this.ours || this.disposed || !this.element) return;
    this.onPause();
  };

  /** `output` is the engine's last node; it is connected to the element's stream or the speakers. */
  constructor(context: StreamContext, output: AudioNode, onPause: () => void, make: () => CarrierElement | null = defaultElement) {
    this.context = context; this.output = output; this.onPause = onPause;
    let element: CarrierElement | null = null, stream: MediaStreamAudioDestinationNode | null = null;
    try {
      // MediaStreamAudioDestination defaults to stereo. Surround uses the real endpoint.
      if (configureDestination(context.destination) <= 2 && typeof context.createMediaStreamDestination === 'function') {
        element = make();
        if (element) {
          stream = context.createMediaStreamDestination();
          element.setAttribute('playsinline', '');
          element.srcObject = stream.stream;
        }
      }
    } catch { element = null; stream = null; }
    if (element && stream) {
      this.element = element; this.stream = stream;
      output.connect(stream);
      element.addEventListener('pause', this.paused);
    } else output.connect(context.destination);
  }

  /** Re-read the actual endpoint when the selected/default output changes. Never fold surround to stereo. */
  refreshOutput(): number {
    const channels = configureDestination(this.context.destination);
    if (channels > 2) this.direct();
    return channels;
  }

  /** Whether the audio goes through the element (and so reaches media keys and the lock screen). */
  get carrying(): boolean { return this.element !== null; }

  /** Playing: the element plays its stream. One that refuses gives the speakers the audio directly. */
  play(onRefused?: (error: unknown) => void): void {
    const element = this.element;
    if (!element || !element.paused) return;
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
    try { element.pause(); } finally { this.ours = false; }
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
    try { this.output.disconnect(); } catch { /* already */ }
    this.output.connect(this.context.destination);
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
    try { this.stream?.disconnect(); } catch { /* already */ }
    this.stream = null;
  }
}

function defaultElement(): CarrierElement | null {
  if (typeof document === 'undefined' || typeof document.createElement !== 'function') return null;
  return document.createElement('audio') as unknown as CarrierElement;
}
