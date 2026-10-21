import { log } from '../log/index.ts';
import type { StoreContext } from './layout.ts';

/** Tells the listeners that usage was recorded; a listener that throws is logged and does not stop the others. */
export function notifyUsage(ctx: StoreContext): void {
  for (const listener of [...ctx.usageListeners]) {
    try {
      listener();
    } catch (err) {
      log('error', 'store', 'usage listener failed', { error: String(err) });
    }
  }
}
