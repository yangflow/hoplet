// Images pasted, dropped or attached to a prompt (docs/spec.md#hoplet, ADR 0017): PNG, JPEG, GIF or WebP, at
// most 4, each at most 4 MiB and together at most 8 MiB. The core checks the same again.
import {
  PROMPT_IMAGE_MAX_BYTES,
  PROMPT_IMAGES_MAX,
  PROMPT_IMAGES_MAX_BYTES,
  type PromptImage,
  sniffImageType,
} from '@hoplet/shared';
import { t } from '../../../lib/i18n.ts';

/** An image waiting in the composer: what goes to the core, its name and its size for the thumbnail. */
export type Attachment = PromptImage & { id: string; name: string; size: number };

/** The base64 of bytes, in chunks so a large image does not overflow the argument list. */
export function toBase64(bytes: Uint8Array): string {
  let binary = '';
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary);
}

/** The image as a data URL, for its thumbnail. */
export function dataUrl(image: PromptImage): string {
  return `data:${image.mediaType};base64,${image.data}`;
}

/** The size in bytes of base64 data. */
export function sizeOf(data: string): number {
  const pad = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0;
  return (data.length / 4) * 3 - pad;
}

let counter = 0;

/** Reads a file into an attachment, or says why it cannot be one. The type is read from its bytes. */
export async function readImage(file: File): Promise<Attachment | string> {
  const name = file.name || t('Pasted image');
  if (file.size > PROMPT_IMAGE_MAX_BYTES) {
    return t('{name} is larger than 4 MiB').replace('{name}', name);
  }
  const bytes = new Uint8Array(await file.arrayBuffer());
  const mediaType = sniffImageType(bytes.subarray(0, 16));
  if (!mediaType || bytes.length === 0) return t('Only PNG, JPEG, GIF and WebP images');
  counter += 1;
  return { id: `a${counter}`, name, size: bytes.length, mediaType, data: toBase64(bytes) };
}

/** Why `next` cannot join `current`, or null: the count and the total size are limited. */
export function refusal(current: readonly Attachment[], next: Attachment): string | null {
  if (current.length >= PROMPT_IMAGES_MAX) return t('At most 4 images');
  const total = current.reduce((n, a) => n + a.size, 0) + next.size;
  if (total > PROMPT_IMAGES_MAX_BYTES) return t('The images are larger than 8 MiB together');
  return null;
}

/** What goes to the core: the media type and the data of each attachment. */
export function promptImages(attachments: readonly Attachment[]): PromptImage[] {
  return attachments.map(({ mediaType, data }) => ({ mediaType, data }));
}

/** Attachments again from images that came back with a refused prompt. */
export function fromImages(images: readonly PromptImage[]): Attachment[] {
  return images.map((image) => {
    counter += 1;
    return { ...image, id: `a${counter}`, name: t('Image'), size: sizeOf(image.data) };
  });
}
