/**
 * The bulk class of core work (docs/architecture.md#principles): history pages, searches and sync
 * appends. Control work never goes through this queue; it is served as soon as it arrives. The queue
 * bounds how much bulk work is in flight and waiting, and starts each task on a fresh turn of the event
 * loop so control work that arrived meanwhile runs first.
 */
export class BulkBusyError extends Error {
  constructor() {
    super('bulk queue full');
  }
}

export interface BulkQueueOptions {
  /** Tasks running at once; a task that waits on file I/O leaves room for the next. */
  concurrency: number;
  /** Tasks waiting beyond those running; one more is refused with BulkBusyError. */
  maxWaiting: number;
}

export interface BulkQueue {
  run<T>(task: () => Promise<T> | T): Promise<T>;
  /** Tasks running or waiting. */
  readonly size: number;
}

export function createBulkQueue(options: BulkQueueOptions): BulkQueue {
  const waiting: (() => void)[] = [];
  let running = 0;

  function next(): void {
    while (running < options.concurrency) {
      const start = waiting.shift();
      if (!start) return;
      running++;
      start();
    }
  }

  return {
    get size() {
      return running + waiting.length;
    },
    run<T>(task: () => Promise<T> | T): Promise<T> {
      if (waiting.length >= options.maxWaiting && running >= options.concurrency) {
        return Promise.reject(new BulkBusyError());
      }
      return new Promise<T>((resolve, reject) => {
        waiting.push(() => {
          // A fresh turn: requests already received are handled before the task begins.
          setImmediate(() => {
            Promise.resolve()
              .then(task)
              .then(resolve, reject)
              .finally(() => {
                running--;
                next();
              });
          });
        });
        next();
      });
    },
  };
}
