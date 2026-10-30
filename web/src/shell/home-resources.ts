import type {HomeDocument} from '@core/home.ts';
import type {HomeService} from '@core/home-service.ts';

type Resource<T> = Readonly<{value: T; dispose: () => void}>;
/** Loading/error is usable shell state. Private media resources need a genuine Home fence. */
export class HomeDocumentResources<T> {
  private resource?: Resource<T>;
  private fence?: string;
  private closed = false;
  private building = false;
  private off?: () => void;
  constructor(private readonly home: Pick<HomeService, 'subscribe' | 'getSnapshot'>, private readonly options: Readonly<{
    isCurrent: () => boolean;
    create: (document: HomeDocument) => Resource<T>;
    changed: () => void;
    failed: (error: unknown) => void;
  }>) {
    const off = home.subscribe(this.sync);
    if (this.closed) off();else this.off = off;
    this.sync();
  }
  get = (): T | undefined => !this.closed && this.options.isCurrent() && this.resource && this.home.getSnapshot().document?.viewerFence === this.fence ? this.resource.value : undefined;
  private sync = () => {
    if (this.closed) return;
    const document = this.home.getSnapshot().document;
    if (!this.options.isCurrent() || this.fence !== undefined && document?.viewerFence !== this.fence) {this.dispose();this.options.changed();return;}
    if (!document || this.resource || this.building) return;
    let next: Resource<T>;
    this.building = true;
    try {next = this.options.create(document);} catch (error) {this.dispose();this.options.failed(error);this.options.changed();return;}
    finally {this.building = false;}
    if (this.closed || !this.options.isCurrent() || this.home.getSnapshot().document?.viewerFence !== document.viewerFence) {next.dispose();this.dispose();this.options.changed();return;}
    this.fence = document.viewerFence;this.resource = next;this.options.changed();
  };
  dispose = () => {
    if (this.closed) return;
    this.closed = true;this.off?.();this.off = undefined;
    const resource = this.resource;this.resource = undefined;resource?.dispose();
  };
}
