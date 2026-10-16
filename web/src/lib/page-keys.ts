// While Settings is open the cockpit stays mounted, hidden and inert, so its drafts and scroll survive; its
// window-level key handlers must not act on a page nobody sees.
let suspended = false;

export function setPageKeysSuspended(value: boolean): void {
  suspended = value;
}

export function pageKeysSuspended(): boolean {
  return suspended;
}
