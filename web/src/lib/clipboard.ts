/** Puts text on the clipboard; resolves whether it worked (a browser may refuse without a user gesture). */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through to the selection route */
  }
  try {
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.append(area);
    area.select();
    const ok = document.execCommand('copy');
    area.remove();
    return ok;
  } catch {
    return false;
  }
}

/** How a copy of text that is not at hand yet ended. */
export type CopyResult = 'copied' | 'empty' | 'failed';

class Nothing extends Error {}

/**
 * Copies text that has to be fetched first. Safari and Firefox take a clipboard write only inside the user's gesture,
 * so the write starts at once with a promise of the text (`ClipboardItem` accepts one) and ends when the text
 * arrives; where that is not available the text is awaited and written as `copyText` does. `get` resolving `null`
 * means there is nothing to copy.
 */
export async function copyLater(get: () => Promise<string | null>): Promise<CopyResult> {
  const canPromise =
    typeof navigator !== 'undefined' &&
    typeof ClipboardItem !== 'undefined' &&
    typeof navigator.clipboard?.write === 'function';
  const pending = get();
  if (canPromise) {
    const blob = pending.then((text) => {
      if (text === null) throw new Nothing();
      return new Blob([text], { type: 'text/plain' });
    });
    try {
      await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]);
      return 'copied';
    } catch (e) {
      if (e instanceof Nothing) return 'empty';
      // A browser that refuses a promise in the item still has the text route below.
    }
  }
  try {
    const text = await pending;
    if (text === null) return 'empty';
    return (await copyText(text)) ? 'copied' : 'failed';
  } catch {
    return 'failed';
  }
}
