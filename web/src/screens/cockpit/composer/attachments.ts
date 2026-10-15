// Images pasted, dropped or attached to a prompt (docs/spec.md#hoplet, ADR 0017). Each is shrunk to at most
// 1568 px on its long edge (a GIF is left as it is), must then be PNG, JPEG, GIF or WebP of at most 4 MiB, and
// is uploaded to the machine at once; the prompt names the uploads. A prompt takes at most 4, together at most
// 8 MiB. The core and the agent check the same again.
import {
  PROMPT_IMAGE_MAX_BYTES,
  PROMPT_IMAGES_MAX,
  PROMPT_IMAGES_MAX_BYTES,
  type PromptImage,
  type PromptImageType,
  sniffImageType,
} from '@hoplet/shared';
import { t } from '../../../lib/i18n.ts';

/** The long edge an image is shrunk to before upload: what Claude reads at full detail. */
export const MAX_EDGE = 1568;
/** A file larger than this is refused before it is read, shrinking or not. */
const MAX_FILE_BYTES = 32 * 1024 * 1024;

/**
 * An image waiting in the composer: what is uploaded, its name and size for the thumbnail, and where its upload
 * stands. A prompt can be sent once every image is `ready`.
 */
export type Attachment = PromptImage & {
  id: string;
  name: string;
  size: number;
  status: 'uploading' | 'ready' | 'failed';
  /** The ID the machine stored it under, once `ready`. */
  attachmentId?: string;
};

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

/** The size an image is drawn at: the long edge at most `MAX_EDGE`, the proportions kept. */
export function shrunkSize(
  width: number,
  height: number,
): { width: number; height: number } | null {
  const scale = MAX_EDGE / Math.max(width, height);
  if (scale >= 1) return null;
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
  };
}

/**
 * The image drawn again at most `MAX_EDGE` on its long edge, in its own type where the browser can write it.
 * A GIF (it may move), an image that is small enough, and a browser without the canvas APIs keep the bytes.
 */
async function shrink(
  bytes: Uint8Array,
  mediaType: PromptImageType,
): Promise<{ bytes: Uint8Array; mediaType: PromptImageType }> {
  if (
    mediaType === 'image/gif' ||
    typeof createImageBitmap === 'undefined' ||
    typeof OffscreenCanvas === 'undefined'
  ) {
    return { bytes, mediaType };
  }
  const bitmap = await createImageBitmap(new Blob([bytes.slice()], { type: mediaType }));
  try {
    const size = shrunkSize(bitmap.width, bitmap.height);
    if (!size) return { bytes, mediaType };
    const canvas = new OffscreenCanvas(size.width, size.height);
    canvas.getContext('2d')?.drawImage(bitmap, 0, 0, size.width, size.height);
    const blob = await canvas.convertToBlob({ type: mediaType, quality: 0.9 });
    const out = new Uint8Array(await blob.arrayBuffer());
    // A browser that cannot write the type writes PNG instead; the bytes say which.
    return { bytes: out, mediaType: sniffImageType(out.subarray(0, 16)) ?? mediaType };
  } finally {
    bitmap.close();
  }
}

let counter = 0;

/** Reads a file into an attachment waiting for its upload, or says why it cannot be one. */
export async function readImage(file: File): Promise<Attachment | string> {
  const name = file.name || t('Pasted image');
  const tooLarge = t('{name} is larger than 4 MiB').replace('{name}', name);
  if (file.size > MAX_FILE_BYTES) return tooLarge;
  const raw = new Uint8Array(await file.arrayBuffer());
  const sniffed = sniffImageType(raw.subarray(0, 16));
  if (!sniffed || raw.length === 0) return t('Only PNG, JPEG, GIF and WebP images');
  let shrunk: { bytes: Uint8Array; mediaType: PromptImageType };
  try {
    shrunk = await shrink(raw, sniffed);
  } catch {
    // The signature is right but the browser cannot decode the rest: a damaged file.
    return t('{name} cannot be read as an image').replace('{name}', name);
  }
  const { bytes, mediaType } = shrunk;
  if (bytes.length > PROMPT_IMAGE_MAX_BYTES) return tooLarge;
  counter += 1;
  return {
    id: `a${counter}`,
    name,
    size: bytes.length,
    mediaType,
    data: toBase64(bytes),
    status: 'uploading',
  };
}

/** Why `next` cannot join `current`, or null: the count and the total size are limited. */
export function refusal(current: readonly Attachment[], next: Attachment): string | null {
  if (current.length >= PROMPT_IMAGES_MAX) return t('At most 4 images');
  const total = current.reduce((n, a) => n + a.size, 0) + next.size;
  if (total > PROMPT_IMAGES_MAX_BYTES) return t('The images are larger than 8 MiB together');
  return null;
}

/** Attachments from images, for development fixtures: ready, as if uploaded. */
export function fromImages(images: readonly PromptImage[]): Attachment[] {
  return images.map((image) => {
    counter += 1;
    return {
      ...image,
      id: `a${counter}`,
      name: t('Image'),
      size: sizeOf(image.data),
      status: 'ready',
      attachmentId: `fixture-${counter}`,
    };
  });
}
