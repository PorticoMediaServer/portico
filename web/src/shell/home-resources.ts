import type {HomeDocument} from '@core/home.ts';
import type {HomeService} from '@core/home-service.ts';

type Resource<T> = Readonly<{value: T; dispose: () => void}>;
/**
 * Home's own resources (its selection) start from the first Home document. Loading and errors before it
 * are usable shell state. Once built, a resource follows later Home documents itself: a moved viewer fence
 * (the viewer's access decided again) is a newer answer, and it neither rebuilds nor closes the resource.
 * Only the shell's retirement, or a setup that failed, ends it.
 */
export class HomeDocumentResources<T> {
  private resource?: Resource<T>;
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
  get = (): T | undefined => !this.closed && this.options.isCurrent() ? this.resource?.value : undefined;
  private sync = () => {
    if (this.closed) return;
    if (!this.options.isCurrent()) {this.dispose();this.options.changed();return;}
    const document = this.home.getSnapshot().document;
    if (!document || this.resource || this.building) return;
    let next: Resource<T>;
    this.building = true;
    try {next = this.options.create(document);} catch (error) {this.dispose();this.options.failed(error);this.options.changed();return;}
    finally {this.building = false;}
    if (this.closed || !this.options.isCurrent()) {next.dispose();this.dispose();this.options.changed();return;}
    this.resource = next;this.options.changed();
  };
  dispose = () => {
    if (this.closed) return;
    this.closed = true;this.off?.();this.off = undefined;
    const resource = this.resource;this.resource = undefined;resource?.dispose();
  };
}
